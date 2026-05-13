// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"errors"
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
		printPlan(&buf, []PlanEntry{{
			Module: modules.Module{Dir: "cli"}, OldVersion: "0.1.0", Reason: "no commits",
		}})
		out := buf.String()
		if !strings.Contains(out, "(skip)") {
			t.Fatalf("output missing (skip): %q", out)
		}
	})

	t.Run("full entry renders the OLD -> NEW tag form", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		printPlan(&buf, []PlanEntry{{
			Module:     modules.Module{Dir: "cli"},
			OldVersion: "0.1.0", NewVersion: "0.2.0",
			Tag: "cli/v0.2.0", Reason: "feat",
		}})
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
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(runner.calls))
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
		runner := &planFakeRunner{runErr: errors.New("tag exists")}
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
// inject one body per Run invocation in order.
type planFakeRunner struct {
	mu             sync.Mutex
	calls          []gitCall
	output         string
	perCallOutputs []string
	runErr         error
}

func (f *planFakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.mu.Lock()
	idx := len(f.calls)
	f.calls = append(f.calls, gitCall{name: name, args: append([]string(nil), args...)})
	body := f.output
	if idx < len(f.perCallOutputs) {
		body = f.perCallOutputs[idx]
	}
	f.mu.Unlock()
	if opts.Stdout != nil && body != "" {
		_, _ = opts.Stdout.Write([]byte(body))
	}
	return f.runErr
}

func (*planFakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}
