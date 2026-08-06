// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/checks/commitmsg"
	"go.thesmos.sh/ergon/internal/modules"
)

// TestPipelineGitOpsBypassHooks pins the hook boundary: the
// pipeline's own commits and pushes carry --no-verify. Development
// hooks assert workspace-wide invariants (every module tidy) that
// are only true at the pipeline's entry and exit — mid-release, a
// dependent module legitimately pins a sibling version older than
// its imports were built against, so a commit-time gate fails by
// construction. The gate runs where its invariant holds: before
// the release starts.
func TestPipelineGitOpsBypassHooks(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	runner := &gitFakeRunner{}

	if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}

	commits, pushes := 0, 0
	for _, c := range runner.calls {
		if c.name != "git" || len(c.args) == 0 {
			continue
		}
		switch c.args[0] {
		case "commit":
			commits++
			if !slices.Contains(c.args, "--no-verify") {
				t.Errorf("commit args = %v, want --no-verify", c.args)
			}
			// The message reaches git as the argument after -m: a
			// constant subject with the tag list in the body, so
			// multi-tag layers cannot overflow max_subject_length.
			mi := slices.Index(c.args, "-m")
			if mi < 0 || mi+1 >= len(c.args) {
				t.Fatalf("commit args = %v, want -m <msg>", c.args)
			}
			msg := c.args[mi+1]
			if first, _, _ := strings.Cut(msg, "\n"); first != "chore(release): pin intra-workspace deps" {
				t.Errorf("commit subject = %q, want the constant subject", first)
			}
			if !strings.Contains(msg, "\n  - sub/v0.1.1") {
				t.Errorf("commit msg = %q, want the layer's tag in the body", msg)
			}
		case "push":
			pushes++
			if !slices.Contains(c.args, "--no-verify") {
				t.Errorf("push args = %v, want --no-verify", c.args)
			}
		}
	}
	if commits == 0 || pushes == 0 {
		// A zero count would make every assertion above vacuous.
		t.Fatalf("commits=%d pushes=%d, want both exercised", commits, pushes)
	}
}

// TestPinCommitMessage pins the rendered shape and its conformance
// to ergon's own default commit-message gate. The eidos incident:
// the old format put the tag list in the subject, so a five-tag
// layer overflowed max_subject_length and the repository's
// commit-msg hook rejected the pipeline's own commit.
func TestPinCommitMessage(t *testing.T) {
	t.Parallel()

	msg := PinCommitMessage([]string{"bridge/protogo/v1.3.1", "cmd/eidos-reference/v1.3.1"})
	first, rest, _ := strings.Cut(msg, "\n")
	if first != "chore(release): pin intra-workspace deps" {
		t.Errorf("subject = %q, want the constant subject", first)
	}
	if !strings.HasPrefix(rest, "\n") {
		t.Errorf("msg = %q, want a blank line after the subject", msg)
	}
	for _, tag := range []string{"bridge/protogo/v1.3.1", "cmd/eidos-reference/v1.3.1"} {
		if !strings.Contains(msg, "\n  - "+tag) {
			t.Errorf("msg = %q, want %q listed in the body", msg, tag)
		}
	}
	if err := commitmsg.Validate(msg, commitmsg.Defaults()); err != nil {
		t.Errorf("Validate(two tags) = %v, want nil", err)
	}

	// A wide layer must stay valid: the subject is constant and
	// each body line holds one short tag.
	many := make([]string, 40)
	for i := range many {
		many[i] = fmt.Sprintf("module%02d/v1.2.3", i)
	}
	if err := commitmsg.Validate(PinCommitMessage(many), commitmsg.Defaults()); err != nil {
		t.Errorf("Validate(40 tags) = %v, want nil", err)
	}
}

// threeModuleRepo builds root <- mid <- leaf, the shape the skipped
// module has to be tested against: leaf reaches a released module
// THROUGH a third rather than directly, which is what bit eidos.
func threeModuleRepo(t *testing.T) (root string, mods []modules.Module, plan []PlanEntry) {
	t.Helper()
	root = t.TempDir()
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
	write("go.mod", "module example.test/proj\n\ngo 1.26\n")
	write("mid/go.mod", "module example.test/proj/mid\n\ngo 1.26\n\n"+
		"require example.test/proj v0.1.0\n")
	write("leaf/go.mod", "module example.test/proj/leaf\n\ngo 1.26\n\n"+
		"require (\n\texample.test/proj v0.1.0\n\texample.test/proj/mid v0.1.0\n)\n")

	mods = []modules.Module{{Dir: "."}, {Dir: "mid"}, {Dir: "leaf"}}
	plan = []PlanEntry{
		{
			Module: modules.Module{Dir: "."}, Level: BumpMinor,
			OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0", Reason: "feat",
		},
		{
			Module: modules.Module{Dir: "mid"}, Level: BumpPatch,
			OldVersion: "0.1.0", NewVersion: "0.1.1", Tag: "mid/v0.1.1", Reason: "fix",
		},
		{
			// Skipped: no commits in its own scope. It still depends
			// on both modules that moved.
			Module: modules.Module{Dir: "leaf"}, Level: BumpNone,
			OldVersion: "0.1.0", NewVersion: "0.1.0", Reason: "no commits in module scope",
		},
	}
	return root, mods, plan
}

// TestApplyPipelinePinsSkippedModules covers the direction the
// version map alone does not fix.
//
// A skipped module keeps requiring the versions its siblings sat on
// before the release. Where the workspace resolves siblings through
// local replace directives, `go mod tidy` then raises those
// requirements from local content, so the tree is dirty the moment
// the release finishes and the mod stage of the gate fails.
//
// The rewrite has to land before the FIRST tag, not after the last.
// The release workflow verifies at the tagged commit, so a repair
// commit made after tagging leaves every tag still carrying the
// stale go.mod — the gate keeps failing at all of them and no
// release record is ever created.
func TestApplyPipelinePinsSkippedModules(t *testing.T) {
	t.Parallel()

	root, mods, plan := threeModuleRepo(t)
	runner := &gitFakeRunner{}

	if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(root, "leaf", "go.mod"))
	if err != nil {
		t.Fatalf("read leaf/go.mod: %v", err)
	}
	for _, want := range []string{
		"example.test/proj v0.2.0",
		"example.test/proj/mid v0.1.1",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("leaf/go.mod = %q, want it to require %q", body, want)
		}
	}

	// leaf is never tagged — pinning its requires is not releasing
	// it.
	for _, tag := range tagNamesFrom(runner) {
		if strings.HasPrefix(tag, "leaf/") {
			t.Errorf("tags = %v, want no tag for the skipped module", tagNamesFrom(runner))
		}
	}

	// Ordering is the whole point: leaf/go.mod must be staged
	// before any tag exists, or the tags carry the stale file.
	stagedLeafAt, firstTagAt := -1, -1
	for i, c := range runner.calls {
		if c.name != "git" || len(c.args) == 0 {
			continue
		}
		if stagedLeafAt < 0 && c.args[0] == "add" && slices.Contains(c.args, "leaf/go.mod") {
			stagedLeafAt = i
		}
		if firstTagAt < 0 && c.args[0] == "tag" && slices.Contains(c.args, "-a") {
			firstTagAt = i
		}
	}
	if stagedLeafAt < 0 {
		t.Fatalf("calls = %+v, want leaf/go.mod staged", runner.calls)
	}
	if firstTagAt < 0 || stagedLeafAt > firstTagAt {
		t.Errorf("leaf/go.mod staged at %d, first tag at %d — want the pin before any tag",
			stagedLeafAt, firstTagAt)
	}
}

// TestApplyPipelineAnnouncesBeforeSigning pins the ordering that
// makes a signed release reviewable.
//
// Every commit, tag and push can block on a hardware-key PIN
// prompt. Naming the operation after the call returns means the
// operator reads "Enter PIN for ED25519-SK key:" with nothing on
// screen saying what it is for, and learns what they signed only
// once the signature is given. Each assertion here forces the git
// operation to fail: the announcement can only appear if it was
// written before the call.
func TestApplyPipelineAnnouncesBeforeSigning(t *testing.T) {
	t.Parallel()

	failOn := func(sub string) *gitFakeRunner {
		return &gitFakeRunner{decide: func(_ string, args []string) error {
			if len(args) > 0 && args[0] == sub {
				return errors.New("exit status 1")
			}
			return nil
		}}
	}

	t.Run("the tag is named before git tag runs", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := twoModuleRepo(t)
		var out strings.Builder
		_ = ApplyPipeline(t.Context(), failOn("tag"), root, &out, mods, plan,
			Options{AllowDirty: true, Message: "release"})
		if !strings.Contains(out.String(), "v0.2.0") {
			t.Errorf("output = %q, want the tag named before it is signed", out.String())
		}
	})

	t.Run("the commit is described before git commit runs", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := twoModuleRepo(t)
		var out strings.Builder
		_ = ApplyPipeline(t.Context(), failOn("commit"), root, &out, mods, plan,
			Options{AllowDirty: true, Message: "release"})
		got := out.String()
		if !strings.Contains(got, "commit") {
			t.Errorf("output = %q, want the commit announced before it is signed", got)
		}
		// The module count is what tells the operator the commit is
		// the small mechanical one and not something unexpected.
		if !strings.Contains(got, "sub") {
			t.Errorf("output = %q, want the affected module named", got)
		}
	})

	t.Run("the push is named before git push runs", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := twoModuleRepo(t)
		var out strings.Builder
		_ = ApplyPipeline(t.Context(), failOn("push"), root, &out, mods, plan,
			Options{AllowDirty: true, Message: "release"})
		if !strings.Contains(out.String(), "push") {
			t.Errorf("output = %q, want the push announced before it is signed", out.String())
		}
	})
}

// TestApplyPipelineFailureNamesLayer pins the diagnostic contract
// for mid-pipeline git failures: the error names the layer in
// flight, the failing step, and the resume path. The eidos
// incident died with a bare `exit status 1` — the layer had to be
// reconstructed from tag timestamps.
func TestApplyPipelineFailureNamesLayer(t *testing.T) {
	t.Parallel()

	t.Run("commit failure names the layer and step", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := twoModuleRepo(t)
		runner := &gitFakeRunner{decide: func(_ string, args []string) error {
			if len(args) > 0 && args[0] == "commit" {
				return errors.New("exit status 1")
			}
			return nil
		}}

		err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
			Options{AllowDirty: true, Message: "release"})
		if err == nil {
			t.Fatal("ApplyPipeline returned nil, want the commit failure")
		}
		for _, want := range []string{"sub/v0.1.1", "commit", "re-run"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want it to carry %q", err, want)
			}
		}
	})

	t.Run("push failure names the layer and step", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := twoModuleRepo(t)
		runner := &gitFakeRunner{decide: func(_ string, args []string) error {
			if len(args) > 0 && args[0] == "push" {
				return errors.New("exit status 1")
			}
			return nil
		}}

		err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
			Options{AllowDirty: true, Message: "release"})
		if err == nil {
			t.Fatal("ApplyPipeline returned nil, want the push failure")
		}
		// The first push closes the root layer, so the error names
		// the root's tag.
		for _, want := range []string{"v0.2.0", "push", "re-run"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want it to carry %q", err, want)
			}
		}
	})
}

// TestApplyPipelineSkippedModulePinsDependents pins the resume
// path: a module skipped because it is already tagged (a prior
// run released it, or it simply had no commits) still seeds the
// version map, so dependents released in this run pin the version
// whose content the workspace actually builds against. Without
// this, a resumed release leaves stale sibling pins in the tags it
// creates — eidos's frontend/* tags pin eidos v1.3.0 although
// v1.3.1 was tagged minutes earlier by the run that died.
func TestApplyPipelineSkippedModulePinsDependents(t *testing.T) {
	t.Parallel()

	t.Run("an already-tagged skipped module pins its dependents", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := twoModuleRepo(t)
		plan[0] = PlanEntry{
			Module:     modules.Module{Dir: "."},
			OldVersion: "0.2.0",
			NewVersion: "0.2.0",
			Level:      BumpNone,
			Reason:     "no commits in module scope",
		}
		runner := &gitFakeRunner{}

		if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
			Options{AllowDirty: true, Message: "release"}); err != nil {
			t.Fatalf("ApplyPipeline err: %v", err)
		}

		body, err := os.ReadFile(filepath.Join(root, "sub", "go.mod"))
		if err != nil {
			t.Fatalf("read sub/go.mod: %v", err)
		}
		if !strings.Contains(string(body), "example.test/proj v0.2.0") {
			t.Errorf("sub/go.mod = %q, want the require pinned to the skipped module's released v0.2.0", body)
		}
		if got := tagNamesFrom(runner); !slices.Equal(got, []string{"sub/v0.1.1"}) {
			t.Errorf("tags = %v, want only the dependent's — skipped modules are never re-tagged", got)
		}
	})

	t.Run("a never-tagged skipped module pins nothing", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := twoModuleRepo(t)
		plan[0] = PlanEntry{
			Module:     modules.Module{Dir: "."},
			OldVersion: initialVersion,
			NewVersion: initialVersion,
			Level:      BumpNone,
			Reason:     "no commits in module scope",
		}
		runner := &gitFakeRunner{}

		if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
			Options{AllowDirty: true, Message: "release"}); err != nil {
			t.Fatalf("ApplyPipeline err: %v", err)
		}

		body, err := os.ReadFile(filepath.Join(root, "sub", "go.mod"))
		if err != nil {
			t.Fatalf("read sub/go.mod: %v", err)
		}
		if !strings.Contains(string(body), "example.test/proj v0.1.0") {
			t.Errorf("sub/go.mod = %q, want the require left at v0.1.0 — v0.0.0 is not a version anyone released", body)
		}
	})
}

// TestApplyPipelineAllSkippedNothingToTag guards the exit that the
// version-map seeding must not break: a plan where every entry is
// skipped — including ones with real released versions — performs
// no git operation. The map being non-empty no longer implies
// there is anything to tag.
func TestApplyPipelineAllSkippedNothingToTag(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	plan[0] = PlanEntry{
		Module: modules.Module{Dir: "."}, OldVersion: "0.2.0",
		NewVersion: "0.2.0", Level: BumpNone, Reason: "no commits",
	}
	plan[1] = PlanEntry{
		Module: modules.Module{Dir: "sub"}, OldVersion: "0.1.1",
		NewVersion: "0.1.1", Level: BumpNone, Reason: "no commits",
	}
	runner := &gitFakeRunner{}

	var out strings.Builder
	if err := ApplyPipeline(t.Context(), runner, root, &out, mods, plan,
		Options{AllowDirty: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to tag") {
		t.Errorf("output = %q, want the nothing-to-tag notice", out.String())
	}
	if len(runner.calls) != 0 {
		t.Errorf("git ran %d times, want none for an all-skipped plan", len(runner.calls))
	}
}

// pipelineRepo builds a single-module workspace on disk and returns
// the root plus the module set and a one-entry plan for it.
//
// ApplyPipeline reads go.mod files directly (to rewrite intra-
// workspace requires), so the fixture has to exist on disk even
// though every git call is faked.
func pipelineRepo(t *testing.T) (root string, mods []modules.Module, plan []PlanEntry) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.test/proj\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	mods = []modules.Module{{Dir: "."}}
	plan = []PlanEntry{{
		Module:     modules.Module{Dir: "."},
		Level:      BumpMinor,
		NewVersion: "0.2.0",
		Tag:        "v0.2.0",
		Reason:     "feat commits since v0.1.0",
	}}
	return root, mods, plan
}

// TestApplyPipelineNoTag covers the --no-tag short-circuit: the
// plan is printed and nothing else happens, so no git command runs.
func TestApplyPipelineNoTag(t *testing.T) {
	t.Parallel()
	root, mods, plan := pipelineRepo(t)
	runner := &gitFakeRunner{}

	var out strings.Builder
	if err := ApplyPipeline(t.Context(), runner, root, &out, mods, plan,
		Options{NoTag: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("git ran %d times, want none under --no-tag", len(runner.calls))
	}
	if !strings.Contains(out.String(), "--no-tag") {
		t.Errorf("output = %q, want the skip notice", out.String())
	}
}

// TestApplyPipelineDirtyTree covers the working-tree guard: a dirty
// HEAD aborts before any tag is created, and --allow-dirty bypasses
// the check entirely (so `git status` is never consulted).
func TestApplyPipelineDirtyTree(t *testing.T) {
	t.Parallel()

	t.Run("uncommitted changes abort the run", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := pipelineRepo(t)
		// Non-empty `git status --porcelain` output means dirty.
		runner := &gitFakeRunner{output: " M internal/a.go\n"}

		err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
			Options{Message: "release"})
		if err == nil {
			t.Fatal("ApplyPipeline returned nil, want the dirty-tree refusal")
		}
		if !strings.Contains(err.Error(), "--allow-dirty") {
			t.Errorf("err = %v, want it to name the bypass flag", err)
		}
	})

	t.Run("a failing status check propagates", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := pipelineRepo(t)
		runner := &gitFakeRunner{runErr: errors.New("not a git repository")}

		err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
			Options{Message: "release"})
		if err == nil {
			t.Fatal("ApplyPipeline returned nil, want the git failure")
		}
		if !strings.Contains(err.Error(), "working tree") {
			t.Errorf("err = %v, want it to name the failing step", err)
		}
	})

	t.Run("--allow-dirty skips the check", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := pipelineRepo(t)
		runner := &gitFakeRunner{}

		if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
			Options{AllowDirty: true, NoPush: true, NoBump: true, Message: "release"}); err != nil {
			t.Fatalf("ApplyPipeline err: %v", err)
		}
		for _, c := range runner.calls {
			if len(c.args) > 0 && c.args[0] == "status" {
				t.Error("git status ran, want it skipped under --allow-dirty")
			}
		}
	})
}

// TestApplyPipelineNothingToTag covers the all-skipped plan: every
// entry resolved to BumpNone, so there is no work and the pipeline
// exits before consulting the workspace topology.
func TestApplyPipelineNothingToTag(t *testing.T) {
	t.Parallel()
	root, mods, _ := pipelineRepo(t)
	skipped := []PlanEntry{{
		Module: modules.Module{Dir: "."},
		Level:  BumpNone,
		Reason: "no commits in scope",
	}}
	runner := &gitFakeRunner{}

	var out strings.Builder
	if err := ApplyPipeline(t.Context(), runner, root, &out, mods, skipped,
		Options{AllowDirty: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to tag") {
		t.Errorf("output = %q, want the nothing-to-tag notice", out.String())
	}
}

// TestApplyPipelinePushModes covers the two terminal branches: with
// --no-push the tags stay local and the closing message tells the
// user how to publish them; without it, the pipeline pushes each
// layer so the next layer's tidy can resolve the new tags.
func TestApplyPipelinePushModes(t *testing.T) {
	t.Parallel()

	t.Run("--no-push leaves tags local and says so", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := pipelineRepo(t)
		runner := &gitFakeRunner{}

		var out strings.Builder
		if err := ApplyPipeline(t.Context(), runner, root, &out, mods, plan,
			Options{AllowDirty: true, NoPush: true, NoBump: true, Message: "release"}); err != nil {
			t.Fatalf("ApplyPipeline err: %v", err)
		}
		if !strings.Contains(out.String(), "Tags are local") {
			t.Errorf("output = %q, want the local-tags notice", out.String())
		}
		for _, c := range runner.calls {
			if len(c.args) > 0 && c.args[0] == "push" {
				t.Error("git push ran, want it skipped under --no-push")
			}
		}
	})

	t.Run("the default mode pushes and reports it", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := pipelineRepo(t)
		runner := &gitFakeRunner{}

		var out strings.Builder
		if err := ApplyPipeline(t.Context(), runner, root, &out, mods, plan,
			Options{AllowDirty: true, NoBump: true, Message: "release"}); err != nil {
			t.Fatalf("ApplyPipeline err: %v", err)
		}
		pushed := false
		for _, c := range runner.calls {
			if len(c.args) > 0 && c.args[0] == "push" {
				pushed = true
			}
		}
		if !pushed {
			t.Errorf("calls = %+v, want a git push", runner.calls)
		}
		if !strings.Contains(out.String(), "already pushed") {
			t.Errorf("output = %q, want the pushed notice", out.String())
		}
	})

	t.Run("a failing push aborts", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := pipelineRepo(t)
		runner := &gitFakeRunner{decide: func(_ string, args []string) error {
			if len(args) > 0 && args[0] == "push" {
				return errors.New("exit status 1")
			}
			return nil
		}}

		err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
			Options{AllowDirty: true, NoBump: true, Message: "release"})
		if err == nil {
			t.Fatal("ApplyPipeline returned nil, want the push failure")
		}
		if !strings.Contains(err.Error(), "push") {
			t.Errorf("err = %v, want it to name the failing step", err)
		}
	})
}

// TestApplyPipelineTagFailure covers the tag step: a git failure is
// wrapped with the tag name so the user can see which module's tag
// could not be created.
func TestApplyPipelineTagFailure(t *testing.T) {
	t.Parallel()
	root, mods, plan := pipelineRepo(t)
	runner := &gitFakeRunner{decide: func(_ string, args []string) error {
		if len(args) > 0 && args[0] == "tag" {
			return errors.New("exit status 128")
		}
		return nil
	}}

	err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, NoPush: true, NoBump: true, Message: "release"})
	if err == nil {
		t.Fatal("ApplyPipeline returned nil, want the tag failure")
	}
	if !strings.Contains(err.Error(), "v0.2.0") {
		t.Errorf("err = %v, want it to name the tag", err)
	}
}

// twoModuleRepo builds a workspace where `sub` requires the root
// module, so the topological walk produces two layers: the root
// releases first, then `sub` rewrites its require line against the
// root's new version.
func twoModuleRepo(t *testing.T) (root string, mods []modules.Module, plan []PlanEntry) {
	t.Helper()
	root = t.TempDir()
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
	write("go.mod", "module example.test/proj\n\ngo 1.26\n")
	write("sub/go.mod", "module example.test/proj/sub\n\ngo 1.26\n\n"+
		"require example.test/proj v0.1.0\n")

	mods = []modules.Module{{Dir: "."}, {Dir: "sub"}}
	plan = []PlanEntry{
		{
			Module: modules.Module{Dir: "."}, Level: BumpMinor,
			NewVersion: "0.2.0", Tag: "v0.2.0", Reason: "feat",
		},
		{
			Module: modules.Module{Dir: "sub"}, Level: BumpPatch,
			NewVersion: "0.1.1", Tag: "sub/v0.1.1", Reason: "fix",
		},
	}
	return root, mods, plan
}

// TestApplyPipelineRewritesDependentRequires covers the layered
// rewrite: after the root module is tagged, the dependent module's
// `require` line is pinned to the root's new version, tidied,
// committed, and only then tagged. This is the path with the
// highest silent-corruption risk — a dependent tagged against a
// stale require resolves to the wrong version for consumers.
func TestApplyPipelineRewritesDependentRequires(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	runner := &gitFakeRunner{}

	var out strings.Builder
	if err := ApplyPipeline(t.Context(), runner, root, &out, mods, plan,
		Options{AllowDirty: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(root, "sub", "go.mod"))
	if err != nil {
		t.Fatalf("read sub/go.mod: %v", err)
	}
	if !strings.Contains(string(body), "example.test/proj v0.2.0") {
		t.Errorf("sub/go.mod = %q, want the require pinned to v0.2.0", body)
	}
	// Named before the commit is signed, and naming the module
	// rather than counting them: "1 module(s)" told the operator
	// nothing about what they were about to authorise.
	if !strings.Contains(out.String(), "commit  "+pinCommitSubject+"  (sub)") {
		t.Errorf("output = %q, want the commit announced with its module", out.String())
	}

	// Ordering: the root's tag must precede the dependent's.
	order := tagNamesFrom(runner)
	if len(order) != 2 || order[0] != "v0.2.0" || order[1] != "sub/v0.1.1" {
		t.Errorf("tag order = %v, want [v0.2.0 sub/v0.1.1]", order)
	}

	// go mod tidy must run before the dependent is tagged, because
	// the rewritten require has to reach go.sum.
	tidied := false
	for _, c := range runner.calls {
		if c.name == "go" && len(c.args) > 1 && c.args[0] == "mod" && c.args[1] == "tidy" {
			tidied = true
		}
	}
	if !tidied {
		t.Errorf("calls = %+v, want a `go mod tidy` in the bump layer", runner.calls)
	}
}

// TestApplyPipelineNoBumpSkipsRewrite covers --no-bump: tags are
// created at the initial HEAD with no go.mod rewrite, tidy, or
// commit.
func TestApplyPipelineNoBumpSkipsRewrite(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	runner := &gitFakeRunner{}

	if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, NoBump: true, NoPush: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(root, "sub", "go.mod"))
	if err != nil {
		t.Fatalf("read sub/go.mod: %v", err)
	}
	if !strings.Contains(string(body), "example.test/proj v0.1.0") {
		t.Errorf("sub/go.mod = %q, want the require left untouched", body)
	}
	for _, c := range runner.calls {
		if len(c.args) > 0 && c.args[0] == "commit" {
			t.Error("git commit ran, want no bump commit under --no-bump")
		}
	}
}

// TestApplyPipelineSkippedEntry covers a mixed plan: a skipped
// module is treated as already-released so its dependents are not
// blocked, and no tag is created for it.
func TestApplyPipelineSkippedEntry(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	plan[0].Level = BumpNone // root has no qualifying commits
	plan[0].NewVersion = ""
	plan[0].Tag = ""
	runner := &gitFakeRunner{}

	if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, NoPush: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}

	tagged := tagNamesFrom(runner)
	if len(tagged) != 1 || tagged[0] != "sub/v0.1.1" {
		t.Errorf("tags = %v, want only the dependent's tag", tagged)
	}
}

// TestApplyPipelineMalformedGoMod covers the topology read: a
// go.mod the loader rejects aborts before any tag is created,
// naming the module whose file could not be read.
func TestApplyPipelineMalformedGoMod(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	if err := os.WriteFile(filepath.Join(root, "sub", "go.mod"),
		[]byte("this is not a go.mod {{{\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	runner := &gitFakeRunner{}

	err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, NoPush: true, Message: "release"})
	if err == nil {
		t.Fatal("ApplyPipeline returned nil, want the malformed go.mod surfaced")
	}
	if names := tagNamesFrom(runner); len(names) != 0 {
		t.Errorf("tags %v were created despite the malformed go.mod", names)
	}
}

// TestApplyPipelineTidyFailure covers the tidy step: a failure
// aborts the layer before the dependent is tagged, so a module is
// never tagged with an unresolvable go.sum.
func TestApplyPipelineTidyFailure(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	runner := &gitFakeRunner{decide: func(name string, args []string) error {
		if name == "go" && len(args) > 1 && args[0] == "mod" && args[1] == "tidy" {
			return errors.New("exit status 1")
		}
		return nil
	}}

	if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, Message: "release"}); err == nil {
		t.Fatal("ApplyPipeline returned nil, want the tidy failure")
	}
}

// TestApplyPipelineCommitFailure covers the bump-commit step.
func TestApplyPipelineCommitFailure(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	runner := &gitFakeRunner{decide: func(_ string, args []string) error {
		if len(args) > 0 && args[0] == "commit" {
			return errors.New("exit status 1")
		}
		return nil
	}}

	if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, Message: "release"}); err == nil {
		t.Fatal("ApplyPipeline returned nil, want the commit failure")
	}
}

// tagNamesFrom extracts the created tag names from a fake runner's
// call log. [Tag] invokes `git tag -a -m <message> <name>`, so the
// name is the final argument; `git tag -l <name>` probes from
// EnsureTag are filtered out by requiring the -a form.
func tagNamesFrom(r *gitFakeRunner) []string {
	var out []string
	for _, c := range r.calls {
		if len(c.args) >= 2 && c.args[0] == "tag" && c.args[1] == "-a" {
			out = append(out, c.args[len(c.args)-1])
		}
	}
	return out
}

// TestApplyPipelineAlreadyPinned covers the no-op rewrite branch: a
// dependent whose require already names the version being released
// needs no edit, so the layer produces no bump commit.
func TestApplyPipelineAlreadyPinned(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	// Pre-pin sub's require at the version the root is about to get.
	if err := os.WriteFile(filepath.Join(root, "sub", "go.mod"),
		[]byte("module example.test/proj/sub\n\ngo 1.26\n\n"+
			"require example.test/proj v0.2.0\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	runner := &gitFakeRunner{}

	var out strings.Builder
	if err := ApplyPipeline(t.Context(), runner, root, &out, mods, plan,
		Options{AllowDirty: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}
	if strings.Contains(out.String(), "bumped go.mod") {
		t.Errorf("output = %q, want no bump when the require already matches", out.String())
	}
	if names := tagNamesFrom(runner); len(names) != 2 {
		t.Errorf("tags = %v, want both modules still tagged", names)
	}
}

// TestApplyPipelineBumpWithoutPush covers --no-push on a layer that
// does rewrite go.mods: the bump is committed locally but tidy is
// skipped, because the prior layer's tags exist only in this repo
// and the proxy cannot resolve them.
func TestApplyPipelineBumpWithoutPush(t *testing.T) {
	t.Parallel()
	root, mods, plan := twoModuleRepo(t)
	runner := &gitFakeRunner{}

	if err := ApplyPipeline(t.Context(), runner, root, io.Discard, mods, plan,
		Options{AllowDirty: true, NoPush: true, Message: "release"}); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}

	for _, c := range runner.calls {
		if c.name == "go" && len(c.args) > 1 && c.args[0] == "mod" && c.args[1] == "tidy" {
			t.Error("go mod tidy ran, want it skipped under --no-push")
		}
		if len(c.args) > 0 && c.args[0] == "push" {
			t.Error("git push ran, want it skipped under --no-push")
		}
	}
	// The rewrite and its commit still happen.
	body, err := os.ReadFile(filepath.Join(root, "sub", "go.mod"))
	if err != nil {
		t.Fatalf("read sub/go.mod: %v", err)
	}
	if !strings.Contains(string(body), "example.test/proj v0.2.0") {
		t.Errorf("sub/go.mod = %q, want the require still rewritten", body)
	}
}
