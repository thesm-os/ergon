// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// TestSkipped pins the [PlanEntry.Skipped] indicator the renderer
// and applier both branch on: an empty Tag is the sole skip-state
// signal.
func TestSkipped(t *testing.T) {
	t.Parallel()

	if !(PlanEntry{}).Skipped() {
		t.Fatal("empty entry should be skipped")
	}
	if (PlanEntry{Tag: "v1.0.0"}).Skipped() {
		t.Fatal("entry with tag should not be skipped")
	}
}

// TestIncludePathsFor pins the per-module pathspec rule the scoped
// `git log` uses: root → ["."], submodule → [m.Dir].
func TestIncludePathsFor(t *testing.T) {
	t.Parallel()

	if got := includePathsFor(modules.Module{Dir: "."}); !slices.Equal(got, []string{"."}) {
		t.Errorf("root → %+v, want [.]", got)
	}
	if got := includePathsFor(modules.Module{Dir: "cli"}); !slices.Equal(got, []string{"cli"}) {
		t.Errorf("cli → %+v, want [cli]", got)
	}
}

// TestExcludePathsFor pins the scoping rule that keeps a parent
// module's commit log free of its submodules' commits.
func TestExcludePathsFor(t *testing.T) {
	t.Parallel()

	mods := []modules.Module{
		{Dir: "."},
		{Dir: "cli"},
		{Dir: "frontend/golang"},
	}

	root := excludePathsFor(modules.Module{Dir: "."}, mods)
	if !slices.Contains(root, "cli") || !slices.Contains(root, "frontend/golang") {
		t.Errorf("root excludes = %+v, want every submodule", root)
	}

	cli := excludePathsFor(modules.Module{Dir: "cli"}, mods)
	if len(cli) != 0 {
		t.Errorf("cli excludes = %+v, want empty (no nested mods)", cli)
	}

	front := excludePathsFor(modules.Module{Dir: "frontend"}, []modules.Module{
		{Dir: "."},
		{Dir: "frontend"},
		{Dir: "frontend/golang"},
		{Dir: "cli"},
	})
	if !slices.Contains(front, "frontend/golang") {
		t.Errorf("frontend excludes = %+v, want frontend/golang", front)
	}
	if slices.Contains(front, "cli") {
		t.Errorf("frontend excludes = %+v, must not include sibling cli", front)
	}
}

// TestResolveLevel pins the precedence chain the planner walks:
// per-module override → global force → conventional-commit
// inference → skip when nothing applies.
func TestResolveLevel(t *testing.T) {
	t.Parallel()

	m := modules.Module{Dir: "cli"}

	t.Run("per-module override wins over everything", func(t *testing.T) {
		t.Parallel()
		opts := Options{
			Overrides: map[string]BumpLevel{"cli": BumpMajor},
			Force:     BumpPatch,
		}
		level, _, skip, err := resolveLevel(t.Context(), &planFakeRunner{},
			"/repo", m, []modules.Module{m}, opts, "")
		if err != nil {
			t.Fatalf("resolveLevel err: %v", err)
		}
		if level != BumpMajor || skip != "" {
			t.Fatalf("level=%v skip=%q, want BumpMajor / no-skip", level, skip)
		}
	})

	t.Run("override of none registers as an explicit skip", func(t *testing.T) {
		t.Parallel()
		opts := Options{Overrides: map[string]BumpLevel{"cli": BumpNone}}
		level, _, skip, err := resolveLevel(t.Context(), &planFakeRunner{},
			"/repo", m, []modules.Module{m}, opts, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if level != BumpNone || skip == "" {
			t.Fatalf("level=%v skip=%q, want explicit none-skip", level, skip)
		}
	})

	t.Run("--major force applies when no override matches", func(t *testing.T) {
		t.Parallel()
		opts := Options{Force: BumpMajor}
		level, _, skip, err := resolveLevel(t.Context(), &planFakeRunner{},
			"/repo", m, []modules.Module{m}, opts, "v0.1.0")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if level != BumpMajor || skip != "" {
			t.Fatalf("level=%v skip=%q, want BumpMajor / no skip", level, skip)
		}
	})

	t.Run("conventional commit inference picks BumpMinor on feat", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: "feat: add x\n\n" + commitTrailerSentinel + "\n"}
		level, _, skip, err := resolveLevel(t.Context(), runner,
			"/repo", m, []modules.Module{m}, Options{}, "v0.1.0")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if level != BumpMinor || skip != "" {
			t.Fatalf("level=%v skip=%q, want BumpMinor", level, skip)
		}
	})

	t.Run("no commits + prior tag returns skip with since-clause", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: ""}
		level, _, skip, err := resolveLevel(t.Context(), runner,
			"/repo", m, []modules.Module{m}, Options{}, "v0.1.0")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if level != BumpNone || !strings.Contains(skip, "since v0.1.0") {
			t.Fatalf("level=%v skip=%q, want skip mentioning prior tag", level, skip)
		}
	})

	t.Run("no commits + no tag returns initial-release skip", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: ""}
		level, _, skip, err := resolveLevel(t.Context(), runner,
			"/repo", m, []modules.Module{m}, Options{}, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if level != BumpNone || !strings.Contains(skip, "no prior tag") {
			t.Fatalf("level=%v skip=%q, want initial-release skip", level, skip)
		}
	})

	t.Run("commits with no prior tag produces initial-release inference", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: "feat: x\n\n" + commitTrailerSentinel + "\n"}
		level, reason, skip, err := resolveLevel(t.Context(), runner,
			"/repo", m, []modules.Module{m}, Options{}, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if level != BumpMinor || skip != "" {
			t.Fatalf("level=%v skip=%q, want BumpMinor", level, skip)
		}
		if !strings.Contains(reason, "initial release") {
			t.Fatalf("reason = %q, want initial-release reason", reason)
		}
	})

	t.Run("git failure propagates wrapped", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{runErr: errors.New("git missing")}
		_, _, _, err := resolveLevel(t.Context(), runner,
			"/repo", m, []modules.Module{m}, Options{}, "")
		if err == nil {
			t.Fatal("err = nil, want non-nil")
		}
	})
}

// TestPlanEntryFor exercises the per-module pipeline: tag lookup
// + version parse + level resolution + new-version computation.
func TestPlanEntryFor(t *testing.T) {
	t.Parallel()

	m := modules.Module{Dir: "cli"}

	t.Run("no prior tag + feat commits produces 0.1.0", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{
			perCallOutputs: []string{"", "feat: x\n\n" + commitTrailerSentinel + "\n"},
		}
		entry, err := planEntryFor(t.Context(), runner, "/repo", m,
			[]modules.Module{m}, Options{})
		if err != nil {
			t.Fatalf("planEntryFor err: %v", err)
		}
		if entry.OldVersion != initialVersion {
			t.Fatalf("OldVersion = %q, want %q", entry.OldVersion, initialVersion)
		}
		if entry.NewVersion != "0.1.0" {
			t.Fatalf("NewVersion = %q, want 0.1.0", entry.NewVersion)
		}
		if !strings.HasSuffix(entry.Tag, "v0.1.0") {
			t.Fatalf("Tag = %q, want suffix v0.1.0", entry.Tag)
		}
	})

	t.Run("prior tag + override produces the next version", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{
			perCallOutputs: []string{"cli/v0.1.0\n"},
		}
		entry, err := planEntryFor(t.Context(), runner, "/repo", m,
			[]modules.Module{m}, Options{
				Overrides: map[string]BumpLevel{"cli": BumpMinor},
			})
		if err != nil {
			t.Fatalf("planEntryFor err: %v", err)
		}
		if entry.OldVersion != "0.1.0" {
			t.Fatalf("OldVersion = %q, want 0.1.0", entry.OldVersion)
		}
		if entry.NewVersion != "0.2.0" {
			t.Fatalf("NewVersion = %q, want 0.2.0", entry.NewVersion)
		}
	})

	t.Run("override=none flags the entry as skipped", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{perCallOutputs: []string{"cli/v0.1.0\n"}}
		entry, err := planEntryFor(t.Context(), runner, "/repo", m,
			[]modules.Module{m}, Options{
				Overrides: map[string]BumpLevel{"cli": BumpNone},
			})
		if err != nil {
			t.Fatalf("planEntryFor err: %v", err)
		}
		if !entry.Skipped() {
			t.Fatalf("entry = %+v, want skipped", entry)
		}
	})

	t.Run("LastTag git failure surfaces wrapped", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{runErr: errors.New("git missing")}
		_, err := planEntryFor(t.Context(), runner, "/repo", m,
			[]modules.Module{m}, Options{})
		if err == nil {
			t.Fatal("err = nil, want non-nil")
		}
	})

	t.Run("malformed prior tag wraps ErrInvalidSemver", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{perCallOutputs: []string{"v1\n"}}
		_, err := planEntryFor(t.Context(), runner, "/repo", m,
			[]modules.Module{m}, Options{Force: BumpPatch})
		if err == nil {
			t.Fatal("err = nil, want non-nil")
		}
	})
}

// TestBuildPlan covers the orchestrator: per-module fan-out with
// the planner's per-call dispatch order observed in the returned
// slice.
func TestBuildPlan(t *testing.T) {
	t.Parallel()

	t.Run("returns one entry per module in order", func(t *testing.T) {
		t.Parallel()
		mods := []modules.Module{{Dir: "."}, {Dir: "cli"}}
		runner := &planFakeRunner{
			// Each module: LastTag → "" (no prior).
			// resolveLevel is only called when Force is set, so we
			// avoid the scoped-commits call entirely here.
			output: "",
		}
		plan, err := BuildPlan(t.Context(), runner, "/repo", mods,
			Options{Force: BumpPatch})
		if err != nil {
			t.Fatalf("BuildPlan err: %v", err)
		}
		if len(plan) != 2 {
			t.Fatalf("plan = %d, want 2", len(plan))
		}
		if plan[0].Module.Dir != "." || plan[1].Module.Dir != "cli" {
			t.Fatalf("order = %s/%s, want ./cli", plan[0].Module.Dir, plan[1].Module.Dir)
		}
	})

	t.Run("planner error from any module aborts BuildPlan", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{runErr: errors.New("git missing")}
		_, err := BuildPlan(t.Context(), runner, "/repo",
			[]modules.Module{{Dir: "."}}, Options{})
		if err == nil {
			t.Fatal("BuildPlan err = nil, want non-nil")
		}
	})
}

// TestPrintPlan exercises the rendered output along the empty,
// skipped, and full branches.
func TestPrintPlan(t *testing.T) {
	t.Parallel()

	t.Run("empty plan renders the no-modules notice", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		printPlan(&buf, nil)
		if !strings.Contains(buf.String(), "(no modules)") {
			t.Fatalf("output missing notice: %q", buf.String())
		}
	})

	t.Run("skipped entry renders `(skip)`", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		printPlan(&buf, [][]PlanEntry{{{
			Module: modules.Module{Dir: "cli"}, OldVersion: "0.1.0", Reason: "no commits",
		}}})
		out := buf.String()
		if !strings.Contains(out, "(skip)") {
			t.Fatalf("output missing (skip): %q", out)
		}
	})

	t.Run("full entry renders the OLD -> NEW tag form", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		printPlan(&buf, [][]PlanEntry{{{
			Module:     modules.Module{Dir: "cli"},
			OldVersion: "0.1.0", NewVersion: "0.2.0",
			Tag: "cli/v0.2.0", Reason: "feat",
		}}})
		out := buf.String()
		for _, want := range []string{"0.1.0", "0.2.0", "cli/v0.2.0", "feat"} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q: %q", want, out)
			}
		}
	})
}

// TestApplyPlan pins the side effects: tag-create per non-skipped
// entry, the --no-tag escape hatch, and the empty-set message.
func TestApplyPlan(t *testing.T) {
	t.Parallel()

	t.Run("creates one tag per non-skipped entry", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		var buf strings.Builder
		err := ApplyPlan(t.Context(), runner, "/repo", &buf,
			[]PlanEntry{
				{Tag: "v1.0.0"},
				{Tag: ""}, // skipped
				{Tag: "cli/v0.2.0"},
			}, Options{Message: "rel"})
		if err != nil {
			t.Fatalf("ApplyPlan err: %v", err)
		}
		// EnsureTag's existence probe runs `git tag -l` before each
		// real tag creation, so count only the `git tag -a` calls
		// (the actual annotated-tag creations).
		if got := countTagCreates(runner.calls); got != 2 {
			t.Fatalf("real tag creations = %d, want 2", got)
		}
	})

	t.Run("--no-tag prints the notice and creates nothing", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		var buf strings.Builder
		err := ApplyPlan(t.Context(), runner, "/repo", &buf,
			[]PlanEntry{{Tag: "v1.0.0"}}, Options{NoTag: true})
		if err != nil {
			t.Fatalf("ApplyPlan err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %d, want 0", len(runner.calls))
		}
		if !strings.Contains(buf.String(), "--no-tag") {
			t.Fatalf("output missing --no-tag notice: %q", buf.String())
		}
	})

	t.Run("all-skipped plan prints `nothing to tag`", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		var buf strings.Builder
		err := ApplyPlan(t.Context(), runner, "/repo", &buf,
			[]PlanEntry{{}, {}}, Options{Message: "rel"})
		if err != nil {
			t.Fatalf("ApplyPlan err: %v", err)
		}
		if !strings.Contains(buf.String(), "nothing to tag") {
			t.Fatalf("output missing notice: %q", buf.String())
		}
	})

	t.Run("git failure wraps the failing tag name", func(t *testing.T) {
		t.Parallel()
		// EnsureTag runs `git tag -l <name>` first to check
		// existence (returns empty → tag doesn't exist), then
		// `git tag -a` to create it. Fail only the create so the
		// error path exercised is the one inside ApplyPlan's loop.
		runner := &planFakeRunner{decide: func(_ string, args []string) error {
			if len(args) >= 2 && args[0] == "tag" && args[1] == "-a" {
				return errors.New("tag exists")
			}
			return nil
		}}
		var buf strings.Builder
		err := ApplyPlan(t.Context(), runner, "/repo", &buf,
			[]PlanEntry{{Tag: "v1.0.0"}}, Options{Message: "rel"})
		if err == nil {
			t.Fatal("err = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "v1.0.0") {
			t.Fatalf("err = %v, want it to mention the failing tag", err)
		}
	})
}

// planFakeRunner is a concurrency-safe [xexec.Runner] reused
// across the plan tests. `output` echoes for every call;
// `perCallOutputs` lets a multi-stage test (LastTag → ScopedCommits)
// inject one body per Run invocation in order; `decide` lets a
// test inject per-call error decisions (e.g. let the
// preflight-signing probe succeed while a later real tag fails).
type planFakeRunner struct {
	mu             sync.Mutex
	calls          []gitCall
	output         string
	perCallOutputs []string
	runErr         error
	decide         func(name string, args []string) error
}

func (f *planFakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.mu.Lock()
	idx := len(f.calls)
	f.calls = append(f.calls, gitCall{
		name: name,
		args: append([]string(nil), args...),
		env:  append([]string(nil), opts.Env...),
	})
	body := f.output
	if idx < len(f.perCallOutputs) {
		body = f.perCallOutputs[idx]
	}
	decide := f.decide
	f.mu.Unlock()
	if opts.Stdout != nil && body != "" {
		_, _ = opts.Stdout.Write([]byte(body))
	}
	if decide != nil {
		return decide(name, args)
	}
	return f.runErr
}

// countTagCreates counts the `git tag -a -m <msg> <name>` calls
// recorded by [planFakeRunner]. Used by tests that need to
// assert on actual annotated-tag creations without being thrown
// off by [EnsureTag]'s existence-probe `git tag -l` calls.
func countTagCreates(calls []gitCall) int {
	n := 0
	for _, c := range calls {
		if c.name != "git" || len(c.args) < 5 {
			continue
		}
		if c.args[0] == "tag" && c.args[1] == "-a" {
			n++
		}
	}
	return n
}

func (*planFakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// TestBuildPlanVersionPin covers the --version path, which bypasses
// bump-level resolution entirely: every module is pinned at the
// supplied version, `--bump MODULE=none` still exempts a module,
// and a malformed --version is rejected before any tag is planned.
func TestBuildPlanVersionPin(t *testing.T) {
	t.Parallel()

	mods := []modules.Module{{Dir: "."}, {Dir: "sub"}}

	t.Run("pins every module at the supplied version", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{} // no tags: every module starts at 0.0.0
		plan, err := BuildPlan(t.Context(), runner, t.TempDir(), mods,
			Options{Message: "release", Version: "v2.0.0"})
		if err != nil {
			t.Fatalf("BuildPlan err: %v", err)
		}
		if len(plan) != 2 {
			t.Fatalf("plan = %+v, want one entry per module", plan)
		}
		for _, e := range plan {
			if e.NewVersion != "2.0.0" {
				t.Errorf("%s: NewVersion = %q, want 2.0.0", e.Module.Dir, e.NewVersion)
			}
			if !strings.Contains(e.Reason, "--version") {
				t.Errorf("%s: Reason = %q, want it to name the pin", e.Module.Dir, e.Reason)
			}
		}
		if plan[0].Tag != "v2.0.0" || plan[1].Tag != "sub/v2.0.0" {
			t.Errorf("tags = (%q, %q), want the module tag prefixes applied",
				plan[0].Tag, plan[1].Tag)
		}
	})

	t.Run("a bare version without the v prefix is accepted", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		plan, err := BuildPlan(t.Context(), runner, t.TempDir(),
			[]modules.Module{{Dir: "."}}, Options{Message: "release", Version: "v1.2.3"})
		if err != nil {
			t.Fatalf("BuildPlan err: %v", err)
		}
		if plan[0].NewVersion != "1.2.3" {
			t.Errorf("NewVersion = %q, want 1.2.3", plan[0].NewVersion)
		}
	})

	t.Run("--bump MODULE=none exempts a module from the pin", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		plan, err := BuildPlan(t.Context(), runner, t.TempDir(), mods, Options{
			Message:   "release",
			Version:   "v2.0.0",
			Overrides: map[string]BumpLevel{"sub": BumpNone},
		})
		if err != nil {
			t.Fatalf("BuildPlan err: %v", err)
		}
		if plan[1].Tag != "" {
			t.Errorf("sub Tag = %q, want no tag for the exempted module", plan[1].Tag)
		}
		if !strings.Contains(plan[1].Reason, "none") {
			t.Errorf("sub Reason = %q, want it to name the override", plan[1].Reason)
		}
		if plan[0].NewVersion != "2.0.0" {
			t.Errorf("root NewVersion = %q, want the pin still applied", plan[0].NewVersion)
		}
	})

	t.Run("a malformed --version is rejected", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		_, err := BuildPlan(t.Context(), runner, t.TempDir(),
			[]modules.Module{{Dir: "."}}, Options{Message: "release", Version: "not-a-version"})
		if err == nil {
			t.Fatal("BuildPlan returned nil, want the malformed --version rejected")
		}
		if !strings.Contains(err.Error(), "--version") {
			t.Errorf("err = %v, want it to name the flag", err)
		}
	})
}

// TestBuildPlanGitFailure covers the error propagation from the
// per-module tag lookup: BuildPlan aborts rather than planning a
// release against an unknown current version.
func TestBuildPlanGitFailure(t *testing.T) {
	t.Parallel()

	runner := &planFakeRunner{runErr: errors.New("not a git repository")}
	_, err := BuildPlan(t.Context(), runner, t.TempDir(),
		[]modules.Module{{Dir: "."}}, Options{Message: "release"})
	if err == nil {
		t.Fatal("BuildPlan returned nil, want the git failure propagated")
	}
}

// TestPrintPlanDistinguishesPinFromSkip pins the disclosure the dry
// run owes the operator.
//
// A module skipped for having no commits still has its requires
// rewritten and committed, because a sibling it depends on moved.
// Rendering that as `(skip)` says nothing will be written to it,
// which is false — and `--dry-run` is only worth running if it
// describes every file the run will touch.
//
// A skipped module with nothing to rewrite keeps `(skip)`: the two
// states differ, and collapsing them loses the distinction the
// operator is reading for.
func TestPrintPlanDistinguishesPinFromSkip(t *testing.T) {
	t.Parallel()

	plan := []PlanEntry{
		{
			Module: modules.Module{Dir: "."}, Level: BumpMinor,
			OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0", Reason: "feat",
		},
		{
			Module: modules.Module{Dir: "leaf"}, Level: BumpNone,
			OldVersion: "0.1.0", NewVersion: "0.1.0", Reason: "no commits in module scope",
			Pins: []Pin{
				{Path: "example.test/proj", From: "v0.1.0", To: "v0.2.0"},
				{Path: "example.test/proj/mid", From: "v0.1.0", To: "v0.1.1"},
			},
		},
		{
			Module: modules.Module{Dir: "lonely"}, Level: BumpNone,
			OldVersion: "0.3.0", NewVersion: "0.3.0", Reason: "no commits in module scope",
		},
	}

	var out strings.Builder
	printPlan(&out, [][]PlanEntry{plan})
	got := out.String()

	for _, want := range []string{
		"example.test/proj", "v0.1.0 -> v0.2.0",
		"example.test/proj/mid", "v0.1.0 -> v0.1.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to name the pin %q", got, want)
		}
	}

	lonely := ""
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "lonely") {
			lonely = line
		}
	}
	if !strings.Contains(lonely, "(skip)") || strings.Contains(lonely, "pin") {
		t.Errorf("lonely line = %q, want a plain (skip)", lonely)
	}
}

// TestPrintPlanGroupsWaves pins the grouped rendering. The waves are
// the execution order and therefore the signing order: one commit,
// N tags and one push each, every one of which can block on a
// hardware key. Printed flat, a ten-module release is an
// unattributable run of PIN prompts.
func TestPrintPlanGroupsWaves(t *testing.T) {
	t.Parallel()

	waves := [][]PlanEntry{
		{{
			Module: modules.Module{Dir: "."}, Level: BumpMinor,
			OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0", Reason: "feat",
		}},
		{{
			Module: modules.Module{Dir: "mid"}, Level: BumpPatch,
			OldVersion: "0.1.0", NewVersion: "0.1.1", Tag: "mid/v0.1.1", Reason: "dep moved",
			Pins: []Pin{{Path: "example.test/proj", From: "v0.1.0", To: "v0.2.0"}},
		}},
	}

	var out strings.Builder
	printPlan(&out, waves)
	got := out.String()

	for _, want := range []string{
		"2 modules in 2 waves, 2 tags",
		"wave 1", "nothing published yet",
		"wave 2", "after wave 1 is pushed",
		"pin  example.test/proj",
		"signature",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}

	// A single wave keeps the flat listing — wave headings are
	// ceremony on a single-module repository.
	var flat strings.Builder
	printPlan(&flat, waves[:1])
	if strings.Contains(flat.String(), "wave 1") {
		t.Errorf("single-wave output = %q, want no wave heading", flat.String())
	}
}

// TestPrintPlanAligns guards the column layout. The skip branch used
// a literal run of spaces where the tagged branch used a width, so
// the reason column sat under the tag column and a ten-module plan
// read as two interleaved tables.
func TestPrintPlanAligns(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	printPlan(&out, [][]PlanEntry{{
		{
			Module: modules.Module{Dir: "."}, Level: BumpMinor,
			OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0", Reason: "feat",
		},
		{
			Module: modules.Module{Dir: "leaf"}, Level: BumpNone,
			OldVersion: "0.1.0", NewVersion: "0.1.0", Reason: "no commits",
		},
	}})

	var starts []int
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		if i := strings.Index(line, "->"); i >= 0 {
			starts = append(starts, i)
		}
	}
	if len(starts) != 2 {
		t.Fatalf("found %d entry lines, want 2", len(starts))
	}
	if starts[0] != starts[1] {
		t.Errorf("arrow columns at %v, want them aligned", starts)
	}
}

// cascadeRepo builds root <- mid <- leaf plus an unrelated module,
// so the fixture covers a direct dependent, a transitive one, and a
// module that genuinely has nothing to do.
func cascadeRepo(t *testing.T) (string, []modules.Module, []PlanEntry) {
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
	write("go.mod", "module example.test/proj\n\ngo 1.26\n")
	write("mid/go.mod", "module example.test/proj/mid\n\ngo 1.26\n\n"+
		"require example.test/proj v0.1.0\n")
	write("leaf/go.mod", "module example.test/proj/leaf\n\ngo 1.26\n\n"+
		"require example.test/proj/mid v0.1.0\n")
	write("lonely/go.mod", "module example.test/proj/lonely\n\ngo 1.26\n")

	mods := []modules.Module{{Dir: "."}, {Dir: "mid"}, {Dir: "leaf"}, {Dir: "lonely"}}
	plan := []PlanEntry{
		{
			Module: modules.Module{Dir: "."}, Level: BumpMinor,
			OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0", Reason: "feat",
		},
		{
			Module: modules.Module{Dir: "mid"}, Level: BumpNone,
			OldVersion: "0.1.0", NewVersion: "0.1.0", Reason: "no commits in module scope",
		},
		{
			Module: modules.Module{Dir: "leaf"}, Level: BumpNone,
			OldVersion: "0.4.0", NewVersion: "0.4.0", Reason: "no commits in module scope",
		},
		{
			Module: modules.Module{Dir: "lonely"}, Level: BumpNone,
			OldVersion: "0.3.0", NewVersion: "0.3.0", Reason: "no commits in module scope",
		},
	}
	return root, mods, plan
}

// TestPlanWaves pins the grouping the printed plan and the apply
// pipeline must agree on.
//
// The waves are the signing order, so a plan that groups differently
// from what ApplyPipeline executes would mis-state how many hardware
// key prompts are coming and what each authorises — worse than not
// grouping at all, because it reads as authoritative.
func TestPlanWaves(t *testing.T) {
	t.Parallel()

	t.Run("a module waits for everything it requires", func(t *testing.T) {
		t.Parallel()
		root, mods, plan := cascadeRepo(t)

		waves, err := planWaves(root, mods, plan)
		if err != nil {
			t.Fatalf("planWaves: %v", err)
		}

		got := make([][]string, 0, len(waves))
		for _, w := range waves {
			dirs := make([]string, 0, len(w))
			for _, e := range w {
				dirs = append(dirs, e.Module.Dir)
			}
			got = append(got, dirs)
		}

		// root and lonely depend on nothing, so they go first; mid
		// requires root, leaf requires mid.
		want := [][]string{{".", "lonely"}, {"mid"}, {"leaf"}}
		if len(got) != len(want) {
			t.Fatalf("waves = %v, want %v", got, want)
		}
		for i := range want {
			if !slices.Equal(got[i], want[i]) {
				t.Errorf("wave %d = %v, want %v", i+1, got[i], want[i])
			}
		}
	})

	t.Run("a lone module is a single wave and reads no go.mod", func(t *testing.T) {
		t.Parallel()
		// An empty root: reaching the dependency graph at all would
		// fail here, which is what proves the short-circuit.
		waves, err := planWaves(t.TempDir(), nil, []PlanEntry{{
			Module: modules.Module{Dir: "."}, Level: BumpMinor,
			OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0",
		}})
		if err != nil {
			t.Fatalf("planWaves: %v", err)
		}
		if len(waves) != 1 || len(waves[0]) != 1 {
			t.Fatalf("waves = %v, want one wave of one entry", waves)
		}
	})

	t.Run("a cycle degrades to one wave instead of failing", func(t *testing.T) {
		t.Parallel()
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
		write("a/go.mod", "module example.test/a\n\ngo 1.26\n\n"+
			"require example.test/b v0.1.0\n")
		write("b/go.mod", "module example.test/b\n\ngo 1.26\n\n"+
			"require example.test/a v0.1.0\n")

		mods := []modules.Module{{Dir: "a"}, {Dir: "b"}}
		plan := []PlanEntry{
			{Module: modules.Module{Dir: "a"}, OldVersion: "0.1.0", NewVersion: "0.1.1", Tag: "a/v0.1.1"},
			{Module: modules.Module{Dir: "b"}, OldVersion: "0.1.0", NewVersion: "0.1.1", Tag: "b/v0.1.1"},
		}

		// ApplyPipeline reports the cycle with a usable message; the
		// plan's job is to stay printable, not to duplicate that
		// diagnosis or hide the entries behind an error.
		waves, err := planWaves(root, mods, plan)
		if err != nil {
			t.Fatalf("planWaves on a cycle = %v, want the entries ungrouped", err)
		}
		if len(waves) != 1 || len(waves[0]) != 2 {
			t.Errorf("waves = %v, want a single wave holding both entries", waves)
		}
	})

	t.Run("an unreadable go.mod surfaces", func(t *testing.T) {
		t.Parallel()
		_, err := planWaves(t.TempDir(),
			[]modules.Module{{Dir: "a"}, {Dir: "b"}},
			[]PlanEntry{
				{Module: modules.Module{Dir: "a"}, OldVersion: "0.1.0", NewVersion: "0.1.1"},
				{Module: modules.Module{Dir: "b"}, OldVersion: "0.1.0", NewVersion: "0.1.1"},
			})
		if err == nil {
			t.Error("planWaves = nil, want the missing go.mod reported")
		}
	})
}

// TestCascadeDependentsPropagatesErrors pins that neither failure on
// the promotion path is swallowed.
//
// The cascade decides which modules get tagged. A read or parse
// failure that returned silently would drop a module from the plan
// rather than failing the release, which is the quiet form of the
// bug the cascade exists to fix.
func TestCascadeDependentsPropagatesErrors(t *testing.T) {
	t.Parallel()

	t.Run("an unreadable go.mod fails the pin computation", func(t *testing.T) {
		t.Parallel()
		_, err := cascadeDependents(t.TempDir(), []PlanEntry{
			{
				Module: modules.Module{Dir: "."}, Level: BumpMinor,
				OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0",
			},
			{
				Module: modules.Module{Dir: "gone"}, Level: BumpNone,
				OldVersion: "0.1.0", NewVersion: "0.1.0", Reason: "no commits",
			},
		})
		if err == nil {
			t.Error("cascadeDependents = nil, want the read failure reported")
		}
	})

	t.Run("an unparseable current version fails the bump", func(t *testing.T) {
		t.Parallel()
		root, _, plan := cascadeRepo(t)
		// mid requires the root at v0.1.0 while the root moves to
		// v0.2.0, so mid has a pin and reaches the bump — which then
		// has no valid version to advance from.
		for i, e := range plan {
			if e.Module.Dir == "mid" {
				plan[i].OldVersion = "not-a-semver"
			}
		}
		if _, err := cascadeDependents(root, plan); err == nil {
			t.Error("cascadeDependents = nil, want the bump failure reported")
		}
	})
}

// TestCascadeDependentsPromotesOnStalePins pins the resume case.
//
// The first cascade rule asked whether a dependency moved *in this
// plan*. On a resumed release it had moved in the previous run, so
// nothing triggered: a module with a stale require was left skipped,
// its go.mod rewritten and committed, and no tag cut to carry the
// rewrite. That is the untagged-go.mod bug the cascade exists to
// prevent, returning through the recovery path.
//
// The rule is therefore "has pins", not "a dependency moved": if the
// release will rewrite a module's requires, the release must tag it,
// whichever run moved the dependency.
func TestCascadeDependentsPromotesOnStalePins(t *testing.T) {
	t.Parallel()

	root, _, plan := cascadeRepo(t)

	// Stand the root down to "already released, nothing new" — at
	// 0.2.0, the version a previous run tagged and pushed before it
	// died. It no longer moves in this plan, so the old rule saw no
	// trigger; but mid/go.mod still requires v0.1.0, so the release
	// will still rewrite it and must still tag the result.
	for i, e := range plan {
		if e.Module.Dir == "." {
			plan[i].Level = BumpNone
			plan[i].OldVersion = "0.2.0"
			plan[i].NewVersion = "0.2.0"
			plan[i].Tag = ""
			plan[i].Reason = "no commits in module scope"
		}
	}

	got, err := cascadeDependents(root, plan)
	if err != nil {
		t.Fatalf("cascadeDependents: %v", err)
	}

	for _, e := range got {
		if e.Module.Dir != "mid" {
			continue
		}
		if e.Skipped() {
			t.Fatalf("mid = skipped, want it promoted: its go.mod is "+
				"rewritten by this release, so a tag must carry the rewrite "+
				"(reason %q)", e.Reason)
		}
		if e.Level != BumpPatch {
			t.Errorf("mid level = %v, want a patch bump", e.Level)
		}
	}
}

// TestCascadeDependents pins the rule that makes a dependency bump
// consumable.
//
// Rewriting a skipped module's requires and committing without
// tagging leaves the published module pointing at superseded
// siblings: `go get <mod>@latest` returns the old tag, whose go.mod
// still names versions the workspace has moved past. The rewrite
// only reaches a consumer once it is tagged.
func TestCascadeDependents(t *testing.T) {
	t.Parallel()

	root, _, plan := cascadeRepo(t)
	got, err := cascadeDependents(root, plan)
	if err != nil {
		t.Fatalf("cascadeDependents: %v", err)
	}

	by := map[string]PlanEntry{}
	for _, e := range got {
		by[e.Module.Dir] = e
	}

	t.Run("a direct dependent is promoted to patch", func(t *testing.T) {
		t.Parallel()
		if by["mid"].NewVersion != "0.1.1" || by["mid"].Tag != "mid/v0.1.1" {
			t.Errorf("mid = %+v, want 0.1.1 tagged mid/v0.1.1", by["mid"])
		}
		if by["mid"].Skipped() {
			t.Error("mid still reads as skipped; it must be released to be consumable")
		}
	})

	t.Run("promotion is transitive", func(t *testing.T) {
		t.Parallel()
		// leaf requires mid, which had no version change until the
		// pass above gave it one.
		if by["leaf"].NewVersion != "0.4.1" || by["leaf"].Tag != "leaf/v0.4.1" {
			t.Errorf("leaf = %+v, want 0.4.1 tagged leaf/v0.4.1", by["leaf"])
		}
	})

	t.Run("an unrelated module stays skipped", func(t *testing.T) {
		t.Parallel()
		if !by["lonely"].Skipped() {
			t.Errorf("lonely = %+v, want it left alone", by["lonely"])
		}
	})

	t.Run("the reason names the dependency that caused it", func(t *testing.T) {
		t.Parallel()
		if !strings.Contains(by["mid"].Reason, "example.test/proj") {
			t.Errorf("mid reason = %q, want the triggering dependency named", by["mid"].Reason)
		}
	})

	t.Run("a module with its own bump keeps its level", func(t *testing.T) {
		t.Parallel()
		if by["."].Level != BumpMinor || by["."].NewVersion != "0.2.0" {
			t.Errorf("root = %+v, want its own minor preserved", by["."])
		}
	})
}

// TestPinChangesErrors pins the failure paths of the read the plan
// and the apply share. A go.mod that cannot be read or parsed is a
// real condition — a half-written file, a module directory removed
// under a running release — and silently reporting "no pins" would
// let the plan claim a module is untouched while the apply then
// fails on it.
func TestPinChangesErrors(t *testing.T) {
	t.Parallel()

	t.Run("a missing go.mod is an error", func(t *testing.T) {
		t.Parallel()
		_, err := pinChanges(t.TempDir(), "absent", map[string]string{"x": "v1.0.0"})
		if err == nil {
			t.Fatal("pinChanges = nil, want the missing file reported")
		}
	})

	t.Run("a malformed go.mod is an error", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "go.mod"),
			[]byte("this is not a go.mod\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := pinChanges(root, ".", map[string]string{"x": "v1.0.0"}); err == nil {
			t.Fatal("pinChanges = nil, want the parse failure reported")
		}
	})
}

// TestReleasedVersionMapMissingGoMod pins that a module in the plan
// with no go.mod on disk surfaces rather than silently dropping out
// of the version map — a dependent would then keep requiring a
// superseded version with nothing reporting why.
func TestReleasedVersionMapMissingGoMod(t *testing.T) {
	t.Parallel()

	_, err := releasedVersionMap(t.TempDir(),
		map[string]string{"gone": "1.0.0"}, map[string]bool{"gone": true})
	if err == nil {
		t.Fatal("releasedVersionMap = nil, want the missing go.mod reported")
	}
}

// TestAnnotatePinsPropagatesErrors pins that neither read failure is
// swallowed on the way to the printed plan.
func TestAnnotatePinsPropagatesErrors(t *testing.T) {
	t.Parallel()

	t.Run("a module missing from disk fails the version map", func(t *testing.T) {
		t.Parallel()
		// Two entries: a lone module has no sibling to require, so
		// annotatePins short-circuits before reading anything and
		// there is no failure to propagate.
		_, err := annotatePins(t.TempDir(), []PlanEntry{
			{
				Module: modules.Module{Dir: "."}, Level: BumpMinor,
				OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0", Reason: "feat",
			},
			{
				Module: modules.Module{Dir: "gone"}, Level: BumpNone,
				OldVersion: "0.1.0", NewVersion: "0.1.0", Reason: "no commits",
			},
		})
		if err == nil {
			t.Fatal("annotatePins = nil, want the version-map failure propagated")
		}
	})

	t.Run("a never-tagged skipped module still fails on its own go.mod", func(t *testing.T) {
		t.Parallel()
		// initialVersion keeps it out of the version map, so the
		// read that fails is pinChanges' rather than the map's.
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "go.mod"),
			[]byte("module example.test/proj\n\ngo 1.26\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := annotatePins(root, []PlanEntry{
			{
				Module: modules.Module{Dir: "."}, Level: BumpMinor,
				OldVersion: "0.1.0", NewVersion: "0.2.0", Tag: "v0.2.0", Reason: "feat",
			},
			{
				Module: modules.Module{Dir: "absent"}, Level: BumpNone,
				OldVersion: initialVersion, NewVersion: initialVersion, Reason: "no commits",
			},
		})
		if err == nil {
			t.Fatal("annotatePins = nil, want the pinChanges failure propagated")
		}
	})
}

// TestAnnotatePins pins the computation behind the disclosure: the
// plan learns which requires a skipped module will have rewritten
// by reading its go.mod against the finished version map, without
// writing anything.
func TestAnnotatePins(t *testing.T) {
	t.Parallel()

	root, _, plan := threeModuleRepo(t)
	before, err := os.ReadFile(filepath.Join(root, "leaf", "go.mod"))
	if err != nil {
		t.Fatalf("read leaf/go.mod: %v", err)
	}

	annotated, err := annotatePins(root, plan)
	if err != nil {
		t.Fatalf("annotatePins: %v", err)
	}

	var leaf PlanEntry
	for _, e := range annotated {
		if e.Module.Dir == "leaf" {
			leaf = e
		}
	}
	if len(leaf.Pins) != 2 {
		t.Fatalf("leaf pins = %+v, want 2", leaf.Pins)
	}
	for _, p := range leaf.Pins {
		switch p.Path {
		case "example.test/proj":
			if p.From != "v0.1.0" || p.To != "v0.2.0" {
				t.Errorf("pin %+v, want v0.1.0 -> v0.2.0", p)
			}
		case "example.test/proj/mid":
			if p.From != "v0.1.0" || p.To != "v0.1.1" {
				t.Errorf("pin %+v, want v0.1.0 -> v0.1.1", p)
			}
		default:
			t.Errorf("unexpected pin %+v", p)
		}
	}

	// Annotation is what --dry-run runs, so it must not write.
	after, err := os.ReadFile(filepath.Join(root, "leaf", "go.mod"))
	if err != nil {
		t.Fatalf("re-read leaf/go.mod: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("leaf/go.mod changed during annotation:\n%s", after)
	}
}
