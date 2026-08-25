// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/modules"
)

// cyclicRepo builds a workspace whose two modules require each other,
// which Go permits and a topological release cannot order.
func cyclicRepo(t *testing.T, withReplace bool) (string, []modules.Module, []PlanEntry) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	repA := "\nreplace example.test/proj/b => ../b\n"
	repB := "\nreplace example.test/proj/a => ../a\n"
	if !withReplace {
		repA, repB = "", ""
	}
	write("a/go.mod", "module example.test/proj/a\n\ngo 1.26\n\n"+
		"require example.test/proj/b v0.1.0\n"+repA)
	write("b/go.mod", "module example.test/proj/b\n\ngo 1.26\n\n"+
		"require example.test/proj/a v0.1.0\n"+repB)

	mods := []modules.Module{{Dir: "a"}, {Dir: "b"}}
	plan := []PlanEntry{
		{
			Module: modules.Module{Dir: "a"}, Level: BumpMinor,
			OldVersion: "0.1.0", NewVersion: "1.2.0", Tag: "a/v1.2.0",
		},
		{
			Module: modules.Module{Dir: "b"}, Level: BumpMinor,
			OldVersion: "0.1.0", NewVersion: "1.2.0", Tag: "b/v1.2.0",
		},
	}
	return root, mods, plan
}

// TestApplySingleWaveReleasesACycle pins the reason this mode exists.
//
// Go permits a module cycle and resolves it; eidos has one, where
// eidostest requires lang/golang and lang/golang requires eidostest.
// A cycle has no topological order, so the layered pipeline never
// starts — layerReady finds nothing whose dependencies are all
// released — and the release aborts before tagging anything. Pinning
// every module to one known version removes the ordering question,
// which is the only way such a workspace can be released at all.
func TestApplySingleWaveReleasesACycle(t *testing.T) {
	t.Parallel()

	root, mods, plan := cyclicRepo(t, true)
	runner := &gitFakeRunner{}
	opts := Options{AllowDirty: true, Message: "release", Version: "v1.2.0"}

	if err := ApplyPipeline(t.Context(), runner, root, io.Discard,
		mods, plan, opts); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}

	var order []string
	for _, c := range runner.calls {
		if c.name != "git" || len(c.args) == 0 {
			continue
		}
		switch c.args[0] {
		case "tag":
			if !slices.Contains(c.args, "-l") {
				order = append(order, "tag")
			}
		case "push", "commit":
			order = append(order, c.args[0])
		}
	}

	// One commit carrying every pin, then both tags on it, then a
	// single push. No interleaving, because nothing waits.
	want := []string{"commit", "tag", "tag", "push"}
	if !slices.Equal(order, want) {
		t.Fatalf("git order = %v, want %v", order, want)
	}

	for _, d := range []string{"a", "b"} {
		body, err := os.ReadFile(filepath.Join(root, d, "go.mod"))
		if err != nil {
			t.Fatalf("read %s/go.mod: %v", d, err)
		}
		if !strings.Contains(string(body), "v1.2.0") {
			t.Errorf("%s/go.mod = %q, want its sibling pinned to v1.2.0", d, body)
		}
	}
}

// TestApplySingleWaveRefusesUnreplacedSiblings pins the precondition.
//
// Every pin is written before any tag exists, so `go mod tidy` can
// only resolve a sibling through a local replace. Without one it
// queries the proxy for a version minutes away from being created
// and fails with `unknown revision` — the failure that ended an
// earlier eidos release. Refusing up front costs nothing; failing
// mid-run leaves tags behind.
func TestApplySingleWaveRefusesUnreplacedSiblings(t *testing.T) {
	t.Parallel()

	root, mods, plan := cyclicRepo(t, false)
	err := ApplyPipeline(t.Context(), &gitFakeRunner{}, root, io.Discard,
		mods, plan, Options{AllowDirty: true, Message: "r", Version: "v1.2.0"})
	if err == nil {
		t.Fatal("ApplyPipeline = nil, want the missing replace refused")
	}
	for _, want := range []string{"replace", "example.test/proj/b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

// TestTaggable pins that skipped entries produce no tag in single-wave
// mode, the same as everywhere else.
func TestTaggable(t *testing.T) {
	t.Parallel()

	got := taggable([]PlanEntry{
		{Module: modules.Module{Dir: "a"}, Tag: "a/v1.0.0"},
		{Module: modules.Module{Dir: "b"}},
		{Module: modules.Module{Dir: "c"}, Tag: "c/v1.0.0"},
	})
	if want := []string{"a", "c"}; !slices.Equal(got, want) {
		t.Errorf("taggable = %v, want %v", got, want)
	}
}

// TestApplySingleWaveOptions pins the flags that shape the wave.
//
// Each decides whether a git object is created, so an untested one is
// a mode that might write when the operator asked it not to — the
// failure --no-bump and --no-push exist to prevent.
func TestApplySingleWaveOptions(t *testing.T) {
	t.Parallel()

	steps := func(t *testing.T, opts Options) []string {
		t.Helper()
		root, mods, plan := cyclicRepo(t, true)
		runner := &gitFakeRunner{}
		if err := ApplyPipeline(t.Context(), runner, root, io.Discard,
			mods, plan, opts); err != nil {
			t.Fatalf("ApplyPipeline err: %v", err)
		}
		var out []string
		for _, c := range runner.calls {
			if c.name != "git" || len(c.args) == 0 {
				continue
			}
			switch c.args[0] {
			case "tag":
				if !slices.Contains(c.args, "-l") {
					out = append(out, "tag")
				}
			case "push", "commit":
				out = append(out, c.args[0])
			}
		}
		return out
	}

	base := Options{AllowDirty: true, Message: "r", Version: "v1.2.0"}

	t.Run("--no-bump tags without touching go.mod", func(t *testing.T) {
		t.Parallel()
		o := base
		o.NoBump = true
		got := steps(t, o)
		if slices.Contains(got, "commit") {
			t.Errorf("steps = %v, want no commit under --no-bump", got)
		}
		if !slices.Contains(got, "tag") {
			t.Errorf("steps = %v, want the tags still cut", got)
		}
	})

	t.Run("--no-push keeps the release local", func(t *testing.T) {
		t.Parallel()
		o := base
		o.NoPush = true
		got := steps(t, o)
		if slices.Contains(got, "push") {
			t.Errorf("steps = %v, want no push under --no-push", got)
		}
		// The commit still happens: unlike the layered pipeline, tidy
		// here resolves siblings through local replaces and needs no
		// published tag, so there is nothing to defer.
		if !slices.Contains(got, "commit") {
			t.Errorf("steps = %v, want the pin commit still made", got)
		}
	})

	t.Run("an already-pinned workspace makes no empty commit", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := cyclicRepo(t, true)
		for _, d := range []string{"a", "b"} {
			other := "b"
			if d == "b" {
				other = "a"
			}
			// Rewritten wholesale rather than read-modify-write: the
			// fixture's shape is known here, and reading a composed
			// path back trips the path-traversal analyser.
			body := "module example.test/proj/" + d + "\n\ngo 1.26\n\n" +
				"require example.test/proj/" + other + " v1.2.0\n\n" +
				"replace example.test/proj/" + other + " => ../" + other + "\n"
			if err := os.WriteFile(filepath.Join(root, d, "go.mod"),
				[]byte(body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		runner := &gitFakeRunner{}
		if err := ApplyPipeline(t.Context(), runner, root, io.Discard,
			mods, plan, base); err != nil {
			t.Fatalf("ApplyPipeline err: %v", err)
		}
		// `git commit` on an unchanged path set fails outright, so a
		// re-run against a tree already at the target version must not
		// manufacture one.
		for _, c := range runner.calls {
			if c.name == "git" && len(c.args) > 0 && c.args[0] == "commit" {
				t.Errorf("a commit was made with nothing changed: %+v", c.args)
			}
		}
	})
}

// TestUnreplacedSiblingsErrors pins that an unreadable or malformed
// go.mod fails the precondition rather than passing it.
//
// The check decides whether the release may write pins for versions
// that do not exist yet. Treating a file it could not read as
// "nothing missing" would let the run proceed into the `unknown
// revision` failure the check exists to prevent.
func TestUnreplacedSiblingsErrors(t *testing.T) {
	t.Parallel()

	t.Run("a missing go.mod is reported", func(t *testing.T) {
		t.Parallel()
		_, err := unreplacedSiblings(t.TempDir(),
			[]modules.Module{{Dir: "gone"}}, []string{"example.test/x"})
		if err == nil {
			t.Error("unreplacedSiblings = nil, want the missing go.mod reported")
		}
	})

	t.Run("an unparseable go.mod is reported", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "bad"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "bad", "go.mod"),
			[]byte("!! not a go.mod !!\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := unreplacedSiblings(root,
			[]modules.Module{{Dir: "bad"}}, []string{"example.test/x"})
		if err == nil {
			t.Error("unreplacedSiblings = nil, want the parse failure reported")
		}
	})
}

// TestApplySingleWaveGitFailures pins that each git step surfaces
// rather than being swallowed.
//
// A single-wave release creates every tag on one commit, so a
// failure part-way leaves a workspace whose go.mods claim versions
// that were never published. Each step therefore has to stop the run
// and say which one it was; a silent continue would push some tags
// and not others with nothing recording the gap.
func TestApplySingleWaveGitFailures(t *testing.T) {
	t.Parallel()

	for _, step := range []string{"commit", "tag", "push"} {
		t.Run(step+" failure stops the release", func(t *testing.T) {
			t.Parallel()
			root, mods, plan := cyclicRepo(t, true)
			runner := &gitFakeRunner{decide: func(_ string, args []string) error {
				if len(args) > 0 && args[0] == step {
					// `git tag -l` probes for an existing tag and must
					// keep succeeding, or EnsureTag never reaches the
					// creating form this is meant to break.
					if step == "tag" && slices.Contains(args, "-l") {
						return nil
					}
					return errors.New("exit status 1")
				}
				return nil
			}}
			err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
				Options{AllowDirty: true, Message: "r", Version: "v1.2.0"})
			if err == nil {
				t.Fatalf("ApplyPipeline = nil, want the %s failure surfaced", step)
			}
			if !strings.Contains(err.Error(), step) {
				t.Errorf("err = %v, want it to name the %q step", err, step)
			}
		})
	}
}

// TestUnreplacedSiblingsNamesOnlyOffenders pins that a module which
// replaces its siblings properly is not reported alongside one that
// does not — the operator has to know which go.mod to edit.
func TestUnreplacedSiblingsNamesOnlyOffenders(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, m := range []struct{ dir, body string }{
		{"a", "module example.test/proj/a\n\ngo 1.26\n\n" +
			"require example.test/proj/b v0.1.0\n\n" +
			"replace example.test/proj/b => ../b\n"},
		{"b", "module example.test/proj/b\n\ngo 1.26\n\n" +
			"require example.test/proj/a v0.1.0\n"},
	} {
		if err := os.MkdirAll(filepath.Join(root, m.dir), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", m.dir, err)
		}
		if err := os.WriteFile(filepath.Join(root, m.dir, "go.mod"),
			[]byte(m.body), 0o600); err != nil {
			t.Fatalf("write %s: %v", m.dir, err)
		}
	}

	got, err := unreplacedSiblings(root,
		[]modules.Module{{Dir: "a"}, {Dir: "b"}},
		[]string{"example.test/proj/a", "example.test/proj/b"})
	if err != nil {
		t.Fatalf("unreplacedSiblings: %v", err)
	}
	if len(got["a"]) != 0 {
		t.Errorf("a = %v, want nothing — it replaces its sibling", got["a"])
	}
	if want := []string{"example.test/proj/a"}; !slices.Equal(got["b"], want) {
		t.Errorf("b = %v, want %v", got["b"], want)
	}
}

// TestApplySingleWaveTidyFailure pins that a failing `go mod tidy`
// stops the release before any tag is cut.
//
// Tidy is what proves the pins resolve. Continuing past a failure
// would tag a workspace whose go.sum does not match its go.mod, and
// the proxy caches a tag the moment it is fetched.
func TestApplySingleWaveTidyFailure(t *testing.T) {
	t.Parallel()

	root, mods, plan := cyclicRepo(t, true)
	runner := &gitFakeRunner{decide: func(name string, args []string) error {
		if name == "go" && len(args) > 0 && args[0] == "mod" {
			return errors.New("exit status 1")
		}
		return nil
	}}
	err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, Message: "r", Version: "v1.2.0"})
	if err == nil {
		t.Fatal("ApplyPipeline = nil, want the tidy failure surfaced")
	}
	if !strings.Contains(err.Error(), "tidy") {
		t.Errorf("err = %v, want it to name the failing step", err)
	}
	for _, c := range runner.calls {
		cutTag := c.name == "git" && len(c.args) > 0 &&
			c.args[0] == "tag" && !slices.Contains(c.args, "-l")
		if cutTag {
			t.Errorf("a tag was cut after tidy failed: %+v", c.args)
		}
	}
}

// TestApplySingleWaveRefusalNamesOnlyOffenders pins that the refusal
// lists the module that needs editing and not the one beside it.
func TestApplySingleWaveRefusalNamesOnlyOffenders(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, m := range []struct{ dir, body string }{
		{"a", "module example.test/proj/a\n\ngo 1.26\n\n" +
			"require example.test/proj/b v0.1.0\n\n" +
			"replace example.test/proj/b => ../b\n"},
		{"b", "module example.test/proj/b\n\ngo 1.26\n\n" +
			"require example.test/proj/a v0.1.0\n"},
	} {
		if err := os.MkdirAll(filepath.Join(root, m.dir), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", m.dir, err)
		}
		if err := os.WriteFile(filepath.Join(root, m.dir, "go.mod"),
			[]byte(m.body), 0o600); err != nil {
			t.Fatalf("write %s: %v", m.dir, err)
		}
	}
	plan := []PlanEntry{
		{Module: modules.Module{Dir: "a"}, OldVersion: "0.1.0", NewVersion: "1.2.0", Tag: "a/v1.2.0"},
		{Module: modules.Module{Dir: "b"}, OldVersion: "0.1.0", NewVersion: "1.2.0", Tag: "b/v1.2.0"},
	}
	err := ApplyPipeline(t.Context(), &gitFakeRunner{}, root, io.Discard,
		[]modules.Module{{Dir: "a"}, {Dir: "b"}}, plan,
		Options{AllowDirty: true, Message: "r", Version: "v1.2.0"})
	if err == nil {
		t.Fatal("ApplyPipeline = nil, want the refusal")
	}
	if !strings.Contains(err.Error(), "b requires") {
		t.Errorf("err = %v, want it to name b as the offender", err)
	}
	if strings.Contains(err.Error(), "a requires") {
		t.Errorf("err = %v, want a left out — it replaces its sibling", err)
	}
}

// TestApplyPipelineRefusesCycleBeforeTagging is the regression test
// for a partial release.
//
// The layer loop releases whatever it can reach, so a cycle does not
// stop it — it stops it eventually. Modules outside the cycle are
// ready in layer 1 and get tagged and pushed; only the sanity check
// after the loop notices the rest never went. An eidos run reached
// `git tag cli/v2.0.0` on a workspace that could never finish, and
// a published tag cannot be withdrawn once the proxy has it.
//
// So the refusal has to come before the first tag, not after the
// last, and it must name the way out.
func TestApplyPipelineRefusesCycleBeforeTagging(t *testing.T) {
	t.Parallel()

	root, mods, plan := cyclicRepo(t, true)
	// A third module outside the cycle: it is ready immediately and
	// is exactly what the old code tagged before giving up.
	if err := os.MkdirAll(filepath.Join(root, "c"), 0o700); err != nil {
		t.Fatalf("mkdir c: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "c", "go.mod"),
		[]byte("module example.test/proj/c\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write c: %v", err)
	}
	mods = append(mods, modules.Module{Dir: "c"})
	plan = append(plan, PlanEntry{
		Module: modules.Module{Dir: "c"}, Level: BumpMinor,
		OldVersion: "0.1.0", NewVersion: "1.2.0", Tag: "c/v1.2.0",
	})

	runner := &gitFakeRunner{}
	err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, Message: "r"})
	if err == nil {
		t.Fatal("ApplyPipeline = nil, want the cycle refused")
	}
	if _, ok := errors.AsType[*CycleError](err); !ok {
		t.Fatalf("err = %v, want a *CycleError", err)
	}
	if !strings.Contains(err.Error(), "--version") {
		t.Errorf("err = %v, want it to name the way out", err)
	}

	// Nothing may have been written. `c` was releasable and is the
	// tag the old code cut before failing.
	for _, c := range runner.calls {
		if c.name != "git" || len(c.args) == 0 {
			continue
		}
		wrote := c.args[0] == "commit" || c.args[0] == "push" ||
			(c.args[0] == "tag" && !slices.Contains(c.args, "-l"))
		if wrote {
			t.Errorf("git %v ran before the refusal, want nothing written", c.args)
		}
	}
}

// TestModPathsAreGitPathspecs pins the separator these strings use.
//
// They are handed to `git add` as pathspecs, and git pathspecs are
// forward-slash on every host. Built with filepath.Join they came out
// as `leaf\go.mod` on Windows, so the commit staged a name nothing
// else in the pipeline agreed with and the pin never landed — green
// on Linux and macOS, red only on Windows.
//
// The input is deliberately platform-native: on Windows that is a
// backslash path, which is exactly the case that regressed, and on
// Linux it is the ordinary one. Feeding a literal backslash instead
// would assert nothing here, since a backslash is a legal filename
// character on Unix and ToSlash leaves it alone.
func TestModPathsAreGitPathspecs(t *testing.T) {
	t.Parallel()

	got := modPaths([]string{filepath.Join("lang", "golang")})
	want := []string{"lang/golang/go.mod", "lang/golang/go.sum"}
	if !slices.Equal(got, want) {
		t.Errorf("modPaths = %q, want %q", got, want)
	}

	// The announcement derives from the same strings and must stay
	// readable rather than echoing a host separator back.
	if dirs := dirsOf(got); !slices.Equal(dirs, []string{"lang/golang"}) {
		t.Errorf("dirsOf = %q, want [lang/golang]", dirs)
	}
}

// TestChangedSinceUsesSlashPathsOnDisk pins that a slash pathspec is
// still resolvable as a file, which is what makes the conversion
// safe: the pipeline reports git paths but reads real ones.
func TestChangedSinceUsesSlashPathsOnDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "lang", "golang")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.test/x\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	paths := modPaths([]string{filepath.Join("lang", "golang")})
	before := snapshotPaths(root, paths)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.test/x\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got := changedSince(root, paths, before); !slices.Equal(got, []string{"lang/golang/go.mod"}) {
		t.Errorf("changedSince = %q, want the slash path reported", got)
	}
}
