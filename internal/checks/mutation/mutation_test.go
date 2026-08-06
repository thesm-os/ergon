// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/ergon/internal/checks/policy"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/style"
)

// TestRun pins the package's top-level contract: positional targets
// resolve against cfg.Packages, gremlins runs per target, both
// thresholds are enforced, and the closing verdict reflects every
// target's result.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("empty packages short-circuit with a notice", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		var stdout strings.Builder
		err := Run(t.Context(), runner, &stdout, io.Discard, t.TempDir(), Config{}, nil, nil, RunOptions{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %+v, want zero", runner.calls)
		}
		if !strings.Contains(stdout.String(), "no thresholds") {
			t.Fatalf("stdout = %q, want notice", stdout.String())
		}
	})

	t.Run("both thresholds pass returns nil", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{output: gremlinsOutput(95, 95)}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90, Coverage: 90}}}
		var stdout strings.Builder
		err := Run(t.Context(), runner, &stdout, io.Discard, root, cfg, nil, nil, RunOptions{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "every target met") {
			t.Fatalf("stdout = %q, want final-verdict line", stdout.String())
		}
	})

	t.Run("a nested module is excluded and the exclusion is announced", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		// A module rooted inside the layer. gremlins would otherwise
		// walk into it and mutate files whose tests never run, which
		// is how a root layer came to report 22.1% mutator coverage
		// against 100% line coverage on a five-module workspace.
		nestedDir := filepath.Join(root, "foundation", "sub")
		if err := os.MkdirAll(nestedDir, 0o750); err != nil {
			t.Fatalf("mkdir nested module: %v", err)
		}
		if err := os.WriteFile(filepath.Join(nestedDir, "go.mod"),
			[]byte("module example.test/sub\n"), 0o600); err != nil {
			t.Fatalf("write nested go.mod: %v", err)
		}

		runner := &fakeRunner{output: gremlinsOutput(95, 95)}
		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90, Coverage: 90}}}
		var stdout strings.Builder
		if err := Run(t.Context(), runner, &stdout, io.Discard,
			root, cfg, nil, nil, RunOptions{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}

		// Announced, because this is the only exclusion ergon applies
		// that .ergon.yaml does not declare. Without the line a
		// layer's number changes meaning between releases with
		// nothing on screen to explain it.
		if !strings.Contains(stdout.String(), "excluded 1 nested module(s): foundation/sub") {
			t.Errorf("stdout = %q, want the exclusion disclosed", stdout.String())
		}

		// The announcement is worthless if the pattern never reaches
		// gremlins, so assert the invocation too, not just the prose.
		if len(runner.calls) == 0 {
			t.Fatal("no gremlins invocation recorded")
		}
		for _, want := range []string{"--exclude-files", "^foundation/sub/"} {
			if !strings.Contains(runner.calls[0], want) {
				t.Errorf("invocation = %q, want it to carry %q", runner.calls[0], want)
			}
		}
	})

	t.Run("--mutants reports a passing layer too", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90, Coverage: 90}}}
		// Thresholds are met, so the layer passes — yet five mutants
		// survived. Those are the ones worth naming, and the summary
		// alone cannot locate them.
		out := gremlinsOutput(95, 95) +
			"   LIVED       CONDITIONALS_NEGATION at one.go:10:5\n" +
			"   NOT COVERED ARITHMETIC_BASE at two.go:12:5\n"

		run := func(t *testing.T, opts RunOptions) string {
			t.Helper()
			var buf strings.Builder
			if err := Run(t.Context(), &fakeRunner{output: out}, &buf, io.Discard,
				root, cfg, nil, nil, opts); err != nil {
				t.Fatalf("Run err: %v", err)
			}
			return buf.String()
		}

		quiet := run(t, RunOptions{})
		asked := run(t, RunOptions{Verbose: true})

		// The breakdown used to be tied to the verdict, so --mutants
		// printed no mutants whenever the layer passed — which is
		// when a survivor is cheapest to fix. Asking must answer.
		for _, want := range []string{"one.go", "two.go"} {
			if !strings.Contains(asked, want) {
				t.Errorf("--mutants output = %q, want it to name %q", asked, want)
			}
		}
		// The default stays quiet, or every green run grows a wall of
		// text nobody asked for.
		if strings.Contains(quiet, "one.go") {
			t.Errorf("default output = %q, want the breakdown withheld", quiet)
		}
	})

	t.Run("score below threshold fails", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{output: gremlinsOutput(70, 95)}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90, Coverage: 90}}}
		var stderr strings.Builder
		err := Run(t.Context(), runner, io.Discard, &stderr, root, cfg, nil, nil, RunOptions{})
		if err == nil {
			t.Fatal("Run returned nil, want failure")
		}
		if !strings.Contains(stderr.String(), "below") {
			t.Fatalf("stderr = %q, want final-verdict failure", stderr.String())
		}
	})

	t.Run("coverage below threshold fails", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{output: gremlinsOutput(95, 50)}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90, Coverage: 90}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg, nil, nil, RunOptions{})
		if err == nil {
			t.Fatal("Run returned nil, want failure")
		}
	})

	t.Run("omitted coverage defaults to score", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{output: gremlinsOutput(95, 91)}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg, nil, nil, RunOptions{})
		if err != nil {
			t.Fatalf("Run err: %v, want score-default to accept coverage=91", err)
		}
	})

	t.Run("missing directory skips the layer with a notice", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir() // no `foundation` dir
		runner := &fakeRunner{}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90}}}
		var stdout strings.Builder
		err := Run(t.Context(), runner, &stdout, io.Discard, root, cfg, nil, nil, RunOptions{})
		if err == nil {
			t.Fatal("Run returned nil, want no-targets error")
		}
		if !strings.Contains(stdout.String(), "skip — directory missing") {
			t.Fatalf("stdout = %q, want skip notice", stdout.String())
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %+v, want zero (dir missing)", runner.calls)
		}
	})

	t.Run("gremlins with no metrics and an exit error surfaces failure", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{
			output: "gremlins started... no test files\n",
			runErr: errors.New("exit 1"),
		}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg, nil, nil, RunOptions{})
		if err == nil {
			t.Fatal("Run returned nil, want gremlins error")
		}
	})

	t.Run("positional target restricts to one layer", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation", "core")
		runner := &fakeRunner{output: gremlinsOutput(95, 95)}

		cfg := Config{Packages: []Layer{
			{Path: "foundation/...", Score: 90, Coverage: 90},
			{Path: "core/...", Score: 90, Coverage: 90},
		}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg,
			nil, nil, RunOptions{Targets: []string{"core"}})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %+v, want exactly one (core)", runner.calls)
		}
		if !strings.Contains(runner.calls[0], "core") || strings.Contains(runner.calls[0], "foundation") {
			t.Fatalf("call = %q, want core only", runner.calls[0])
		}
	})

	t.Run("positional subpath restricts gremlins path", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "core/kernel/fold")
		writeGoMod(t, root, "core")
		runner := &fakeRunner{output: gremlinsOutput(95, 95)}

		cfg := Config{Packages: []Layer{{Path: "core/...", Score: 90, Coverage: 90}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg,
			nil, nil, RunOptions{Targets: []string{"core/kernel/fold"}})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %+v, want one", runner.calls)
		}
		// The layer owns the go.mod, so gremlins runs from there and
		// the subpath is expressed relative to it.
		if want := filepath.ToSlash(filepath.Join(root, "core")); runner.dirs[0] != want {
			t.Fatalf("dir = %q, want %q", runner.dirs[0], want)
		}
		if runner.targets[0] != "./kernel/fold/" {
			t.Fatalf("target = %q, want './kernel/fold/'", runner.targets[0])
		}
	})

	t.Run("positional target with no matching layer errors", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg,
			nil, nil, RunOptions{Targets: []string{"unknown"}})
		if err == nil {
			t.Fatal("Run returned nil, want unknown-layer error")
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("err = %v, want it to name the bad layer", err)
		}
	})
}

// TestSelectTargets pins the layer-resolution rules: ordered
// whole-tree mode, bare-layer args, layer/subpath args, and the
// unknown-layer error.
func TestSelectTargets(t *testing.T) {
	t.Parallel()

	packages := []Layer{
		{Path: "foundation/...", Score: 90, Coverage: 85},
		{Path: "core/...", Score: 92},
	}

	t.Run("no requested targets returns every layer in declared order", func(t *testing.T) {
		t.Parallel()
		got, err := selectTargets(packages, nil)
		if err != nil {
			t.Fatalf("selectTargets err: %v", err)
		}
		if len(got) != 2 || got[0].Layer != "foundation" || got[1].Layer != "core" {
			t.Fatalf("targets = %+v, want [foundation, core]", got)
		}
		if got[0].Coverage != 85 || got[1].Coverage != 92 {
			t.Fatalf("coverage thresholds = (%d, %d), want (85, 92)", got[0].Coverage, got[1].Coverage)
		}
	})

	t.Run("bare-layer argument selects the whole layer", func(t *testing.T) {
		t.Parallel()
		got, err := selectTargets(packages, []string{"core"})
		if err != nil {
			t.Fatalf("selectTargets err: %v", err)
		}
		if len(got) != 1 || got[0].Layer != "core" || got[0].Path != "core" || got[0].Label != "core" {
			t.Fatalf("target = %+v, want core whole-layer", got)
		}
	})

	t.Run("layer/subpath sets a repo-relative Path and Label", func(t *testing.T) {
		t.Parallel()
		got, err := selectTargets(packages, []string{"core/kernel/fold"})
		if err != nil {
			t.Fatalf("selectTargets err: %v", err)
		}
		if got[0].Path != "core/kernel/fold" || got[0].Label != "core/kernel/fold" {
			t.Fatalf("target = %+v, want Path=core/kernel/fold Label=core/kernel/fold", got[0])
		}
		// Path is repo-relative, not layer-relative: renderTarget
		// resolves the module root from it and derives the gremlins
		// argument itself.
		if got[0].Layer != "core" {
			t.Fatalf("layer = %q, want core (thresholds come from the layer entry)", got[0].Layer)
		}
	})

	t.Run("a multi-segment layer path is addressable", func(t *testing.T) {
		t.Parallel()
		// Regression: the resolver used to cut at the first "/", so a
		// layer declared as `internal/checks` could only ever be
		// looked up as `internal` — it reported the layer as
		// undeclared while listing it as declared in the same
		// message. Any repo whose layers are not single top-level
		// directories could not target them at all.
		nested := []Layer{
			{Path: "internal/checks", Score: 80, Coverage: 90},
			{Path: "internal/release", Score: 90, Coverage: 88},
		}
		got, err := selectTargets(nested, []string{"internal/checks"})
		if err != nil {
			t.Fatalf("selectTargets err: %v", err)
		}
		if len(got) != 1 || got[0].Layer != "internal/checks" || got[0].Path != "internal/checks" {
			t.Fatalf("target = %+v, want the internal/checks layer", got)
		}
		if got[0].Score != 80 || got[0].Coverage != 90 {
			t.Errorf("thresholds = (%d, %d), want the layer's own (80, 90)",
				got[0].Score, got[0].Coverage)
		}
	})

	t.Run("a subpath under a multi-segment layer keeps that layer's thresholds", func(t *testing.T) {
		t.Parallel()
		nested := []Layer{{Path: "internal/checks", Score: 80, Coverage: 90}}
		got, err := selectTargets(nested, []string{"internal/checks/coverage"})
		if err != nil {
			t.Fatalf("selectTargets err: %v", err)
		}
		if got[0].Layer != "internal/checks" || got[0].Path != "internal/checks/coverage" {
			t.Fatalf("target = %+v, want the subpath under internal/checks", got[0])
		}
		if got[0].Label != "internal/checks/coverage" {
			t.Errorf("label = %q, want the full path", got[0].Label)
		}
	})

	t.Run("longest declared prefix wins for nested layers", func(t *testing.T) {
		t.Parallel()
		// Both claim the path; the more specific one must win so it
		// applies its own thresholds rather than the parent's.
		nested := []Layer{
			{Path: "internal", Score: 10, Coverage: 10},
			{Path: "internal/checks", Score: 80, Coverage: 90},
		}
		got, err := selectTargets(nested, []string{"internal/checks/mutation"})
		if err != nil {
			t.Fatalf("selectTargets err: %v", err)
		}
		if got[0].Layer != "internal/checks" || got[0].Score != 80 {
			t.Fatalf("target = %+v, want the more specific internal/checks entry", got[0])
		}
	})

	t.Run("a prefix that is not a path boundary does not match", func(t *testing.T) {
		t.Parallel()
		// "internal/checksum" must not be claimed by "internal/checks".
		nested := []Layer{{Path: "internal/checks", Score: 80}}
		if _, err := selectTargets(nested, []string{"internal/checksum"}); err == nil {
			t.Fatal("selectTargets returned nil, want no match on a partial segment")
		}
	})

	t.Run("unknown layer surfaces an error listing declared layers", func(t *testing.T) {
		t.Parallel()
		_, err := selectTargets(packages, []string{"unknown"})
		if err == nil {
			t.Fatal("selectTargets returned nil, want error")
		}
		if !strings.Contains(err.Error(), "foundation") || !strings.Contains(err.Error(), "core") {
			t.Fatalf("err = %v, want declared-layer list", err)
		}
	})
}

// TestSplitLayerSubpath pins the layer/subpath splitter against
// the shapes the cobra layer hands in.
func TestSplitLayerSubpath(t *testing.T) {
	t.Parallel()

	declared := []string{"foundation", "core", "internal/checks"}

	cases := []struct {
		in    string
		layer string
		sub   string
		ok    bool
	}{
		{"foundation", "foundation", "", true},
		{"core/kernel/fold", "core", "kernel/fold", true},
		{"core/kernel/fold/", "core", "kernel/fold", true},
		{"/core/kernel", "core", "kernel", true},
		// Multi-segment layer paths resolve against the declared set
		// rather than being cut at the first separator.
		{"internal/checks", "internal/checks", "", true},
		{"internal/checks/mutation", "internal/checks", "mutation", true},
		// No declared layer claims these.
		{"", "", "", false},
		{"unknown", "", "", false},
		{"internal", "", "", false},
		{"internal/checksum", "", "", false},
	}
	for _, tc := range cases {
		gotLayer, gotSub, gotOK := splitLayerSubpath(tc.in, declared)
		if gotLayer != tc.layer || gotSub != tc.sub || gotOK != tc.ok {
			t.Errorf("splitLayerSubpath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, gotLayer, gotSub, gotOK, tc.layer, tc.sub, tc.ok)
		}
	}
}

// TestResolveCoverage pins the single-threshold fallback: Coverage
// defaults to Score when zero, otherwise stands.
func TestResolveCoverage(t *testing.T) {
	t.Parallel()

	if got := resolveCoverage(Layer{Score: 90}); got != 90 {
		t.Errorf("resolveCoverage(Score=90, Coverage=0) = %d, want 90", got)
	}
	if got := resolveCoverage(Layer{Score: 90, Coverage: 85}); got != 85 {
		t.Errorf("resolveCoverage(Score=90, Coverage=85) = %d, want 85", got)
	}
}

// TestParsePercent pins the percentage extractor against gremlins'
// canonical output lines, including the "metric missing" signal.
func TestParsePercent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		out    string
		prefix string
		want   float64
		ok     bool
	}{
		{"floating-point", "Test efficacy: 87.5%\n", "Test efficacy:", 87.5, true},
		{"integer", "Mutator coverage: 92%\n", "Mutator coverage:", 92, true},
		{"prefix missing", "prefix not present", "Test efficacy:", 0, false},
		{"unparseable", "Test efficacy: garbage%\n", "Test efficacy:", 0, false},
		{"last value wins", "Test efficacy: 10%\nTest efficacy: 80%\n", "Test efficacy:", 80, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parsePercent(tc.out, tc.prefix)
			if got != tc.want || ok != tc.ok {
				t.Errorf("parsePercent(%q, %q) = (%v, %v), want (%v, %v)",
					tc.out, tc.prefix, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestParseCounts pins the four-bucket mutant counter against
// gremlins' summary lines.
func TestParseCounts(t *testing.T) {
	t.Parallel()

	out := strings.Join([]string{
		"Killed: 50, Lived: 5, Not covered: 2",
		"Timed out: 1, Not viable: 0, Skipped: 0",
	}, "\n")
	got := parseCounts(out)
	if got.Killed != 50 || got.Lived != 5 || got.NotCovered != 2 || got.TimedOut != 1 {
		t.Fatalf("parseCounts = %+v, want {Killed:50 Lived:5 NotCovered:2 TimedOut:1}", got)
	}
}

// TestParseMutantFiles pins the per-file aggregation: every non-
// killed mutant contributes to its file's total, the result is
// sorted by descending total, and the per-status breakdown adds up.
func TestParseMutantFiles(t *testing.T) {
	t.Parallel()

	out := strings.Join([]string{
		"   LIVED       CONDITIONALS_NEGATION at one.go:10:5",
		"   LIVED       CONDITIONALS_NEGATION at one.go:11:5",
		"   NOT COVERED ARITHMETIC_BASE at one.go:12:5",
		"   TIMED OUT   ARITHMETIC_BASE at two.go:1:1",
		"   LIVED       CONDITIONALS_NEGATION at two.go:2:1",
		"Killed: 100, Lived: 3, Not covered: 1",
	}, "\n")
	got := parseMutantFiles(out)
	if len(got) != 2 {
		t.Fatalf("len(parseMutantFiles) = %d, want 2", len(got))
	}
	if got[0].Path != "one.go" || got[0].Total != 3 {
		t.Errorf("got[0] = %+v, want {one.go total=3}", got[0])
	}
	if got[1].Path != "two.go" || got[1].Total != 2 {
		t.Errorf("got[1] = %+v, want {two.go total=2}", got[1])
	}
	if got[0].Lived != 2 || got[0].NotCovered != 1 {
		t.Errorf("got[0] breakdown = %+v, want L=2 NC=1", got[0])
	}
	if got[1].Lived != 1 || got[1].TimedOut != 1 {
		t.Errorf("got[1] breakdown = %+v, want L=1 TO=1", got[1])
	}
}

// TestFormatElapsed pins the millisecond/second formatting the
// per-target summary line uses.
func TestFormatElapsed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   time.Duration
		want string
	}{
		{42 * time.Millisecond, "[42ms]"},
		{1200 * time.Millisecond, "[1.2s]"},
		{12500 * time.Millisecond, "[12.5s]"},
	}
	for _, tc := range cases {
		if got := formatElapsed(tc.in); got != tc.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestShortStatus pins the 1- or 2-letter tag the verbose
// non-killed-mutant dump prefixes each line with.
func TestShortStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{statusLived, "L"},
		{statusNotCovered, "NC"},
		{statusTimedOut, "TO"},
		{"OTHER", "?"},
	}
	for _, tc := range cases {
		if got := shortStatus(tc.in); got != tc.want {
			t.Errorf("shortStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWriteContributingFiles pins the failure-only file
// breakdown the renderer emits under a failing target. The top-N
// cap collapses surplus rows into a "… and N more file(s)" tail.
func TestWriteContributingFiles(t *testing.T) {
	t.Parallel()

	files := []fileBreakdown{
		{Path: "a.go", Total: 5, Lived: 3, NotCovered: 1, TimedOut: 1},
		{Path: "b.go", Total: 1, Lived: 1},
	}
	var buf strings.Builder
	writeContributingFiles(&buf, style.Style{}, "core", files)
	body := buf.String()
	for _, want := range []string{"Contributing files", "core/a.go", "L:3", "NC:1", "TO:1", "core/b.go"} {
		if !strings.Contains(body, want) {
			t.Fatalf("contributing-files output missing %q: %q", want, body)
		}
	}
}

// TestWriteNonKilledMutants pins the verbose-mode mutant dump:
// every non-killed mutant in the captured gremlins log surfaces
// with its short-status tag and source location.
func TestWriteNonKilledMutants(t *testing.T) {
	t.Parallel()

	files := []fileBreakdown{{Path: "a.go", Total: 1, Lived: 1}}
	gremlinsOut := "    LIVED CONDITIONALS_NEGATION at a.go:5:7\n"
	var buf strings.Builder
	writeNonKilledMutants(&buf, style.Style{}, gremlinsOut, files)
	body := buf.String()
	for _, want := range []string{"Non-killed mutants", "L", "CONDITIONALS_NEGATION", "a.go:5:7"} {
		if !strings.Contains(body, want) {
			t.Fatalf("non-killed-mutants output missing %q: %q", want, body)
		}
	}
}

// TestDefaultWorkers pins the runtime.NumCPU/4 heuristic with a
// floor of 2 — the same shape the bash script used. The pure
// helper [workersFor] is exercised across the floor and above-
// floor branches; [defaultWorkers] is exercised on the host.
func TestDefaultWorkers(t *testing.T) {
	t.Parallel()

	if got := defaultWorkers(); got < 2 {
		t.Errorf("defaultWorkers = %d, want at least 2", got)
	}

	cases := []struct {
		numCPU int
		want   int
	}{
		{1, 2}, // floor
		{4, 2}, // floor (4/4 == 1, below the floor of 2)
		{8, 2}, // floor (8/4 == 2, exactly the floor)
		{12, 3},
		{32, 8},
	}
	for _, tc := range cases {
		if got := workersFor(tc.numCPU); got != tc.want {
			t.Errorf("workersFor(%d) = %d, want %d", tc.numCPU, got, tc.want)
		}
	}
}

// TestLayerDir pins the glob → directory conversion.
func TestLayerDir(t *testing.T) {
	t.Parallel()

	if got := layerDir("foundation/..."); got != "foundation" {
		t.Errorf("layerDir(foundation/...) = %q, want foundation", got)
	}
	if got := layerDir("foo/bar/..."); got != "foo/bar" {
		t.Errorf("layerDir(foo/bar/...) = %q, want foo/bar", got)
	}
}

// fakeRunner satisfies [xexec.Runner] for tests. It records each
// Run invocation as a string of the form `<dir>: <name> <args>`,
// writes `output` to opts.Stdout, and returns runErr from the call.
//
// dirs and targets record the working directory and the final
// positional argument of each call separately, so tests can assert
// on the two independently. A substring match over the joined
// `calls` string cannot distinguish "gremlins ran from the module
// root with ./errs/" from "gremlins ran from errs/ with ." — the
// distinction the working-directory contract turns on.
type fakeRunner struct {
	calls   []string
	dirs    []string
	targets []string
	output  string
	runErr  error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	// filepath.ToSlash normalises Windows backslashes so the call
	// comparisons stay portable across operating systems.
	dir := filepath.ToSlash(opts.Dir)
	f.calls = append(f.calls, dir+": "+name+" "+strings.Join(args, " "))
	f.dirs = append(f.dirs, dir)
	if len(args) > 0 {
		f.targets = append(f.targets, args[len(args)-1])
	} else {
		f.targets = append(f.targets, "")
	}
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(f.output))
	}
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// TestResolveInvocation pins the working-directory contract every
// gremlins call depends on: the tool resolves the module by walking
// up from the filesystem root, not from its working directory, so it
// must be invoked from the directory holding the target's go.mod.
// Invoked from below that, it looks for `/go.mod` and exits 1 before
// doing any work.
func TestResolveInvocation(t *testing.T) {
	t.Parallel()

	t.Run("single-module repo runs from the repo root", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "errs")
		writeGoMod(t, root, ".")

		dir, pkgPath := resolveInvocation(root, "errs")
		if dir != root {
			t.Fatalf("dir = %q, want repo root %q", dir, root)
		}
		if pkgPath != "./errs/" {
			t.Fatalf("path = %q, want './errs/'", pkgPath)
		}
	})

	t.Run("layer owning a go.mod runs from the layer", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "core")
		writeGoMod(t, root, "core")

		dir, pkgPath := resolveInvocation(root, "core")
		if want := filepath.Join(root, "core"); dir != want {
			t.Fatalf("dir = %q, want %q", dir, want)
		}
		if pkgPath != "." {
			t.Fatalf("path = %q, want '.'", pkgPath)
		}
	})

	t.Run("subpath resolves against the nearest enclosing go.mod", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "core/kernel/fold")
		writeGoMod(t, root, ".")
		writeGoMod(t, root, "core")

		// Both the root and `core` carry a go.mod; the nearest one
		// wins so gremlins resolves the module the target belongs to.
		dir, pkgPath := resolveInvocation(root, "core/kernel/fold")
		if want := filepath.Join(root, "core"); dir != want {
			t.Fatalf("dir = %q, want nearest enclosing module %q", dir, want)
		}
		if pkgPath != "./kernel/fold/" {
			t.Fatalf("path = %q, want './kernel/fold/'", pkgPath)
		}
	})

	t.Run("no go.mod anywhere falls back to the repo root", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "errs")

		// gremlins then reports the missing module itself, which is
		// the right diagnostic for a repo that has none.
		dir, pkgPath := resolveInvocation(root, "errs")
		if dir != root {
			t.Fatalf("dir = %q, want repo root %q", dir, root)
		}
		if pkgPath != "./errs/" {
			t.Fatalf("path = %q, want './errs/'", pkgPath)
		}
	})

	t.Run("a go.mod above the repo root is never selected", func(t *testing.T) {
		t.Parallel()
		outer := t.TempDir()
		writeGoMod(t, outer, ".")
		root := filepath.Join(outer, "repo")
		if err := os.MkdirAll(filepath.Join(root, "errs"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		// The walk stops at root; escaping it would run gremlins
		// against an unrelated enclosing module.
		dir, _ := resolveInvocation(root, "errs")
		if dir != root {
			t.Fatalf("dir = %q, want the walk to stop at %q", dir, root)
		}
	})
}

// TestRunGremlinsWorkingDirectory is the regression test for the
// single-module case that could not run at all: every layer reported
// "gremlins did not produce metrics: exit status 1" because gremlins
// was invoked from inside the layer subpackage.
func TestRunGremlinsWorkingDirectory(t *testing.T) {
	t.Parallel()
	root := buildTree(t, "errs", "pool")
	writeGoMod(t, root, ".")
	runner := &fakeRunner{output: gremlinsOutput(99, 99)}

	cfg := Config{Packages: []Layer{
		{Path: "errs", Score: 99, Coverage: 99},
		{Path: "pool", Score: 99, Coverage: 99},
	}}
	if err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg,
		nil, nil, RunOptions{}); err != nil {
		t.Fatalf("Run err: %v", err)
	}

	if len(runner.dirs) != 2 {
		t.Fatalf("calls = %+v, want one per layer", runner.calls)
	}
	for i, layer := range []string{"errs", "pool"} {
		if runner.dirs[i] != filepath.ToSlash(root) {
			t.Errorf("layer %s: dir = %q, want the module root %q",
				layer, runner.dirs[i], filepath.ToSlash(root))
		}
		if want := "./" + layer + "/"; runner.targets[i] != want {
			t.Errorf("layer %s: target = %q, want %q", layer, runner.targets[i], want)
		}
	}
}

// TestTestCPUFlagIsNeverPassed guards the fix for a gremlins defect
// that silently disables the whole gate.
//
// gremlins renders `--test-cpu N` into the per-mutant test command
// as a single argv element containing a space (`-cpu 2`, see
// internal/engine/executor.go). `go test` rejects the malformed
// flag and exits non-zero before compiling; gremlins maps that exit
// to KILLED. Every covered mutant is then "killed" no matter how
// weak the suite is, so the score threshold can never fail.
//
// Verified empirically: a package whose only test asserts nothing
// reports LIVED without the flag and KILLED with it.
func TestTestCPUFlagIsNeverPassed(t *testing.T) {
	t.Parallel()
	root := buildTree(t, "errs")
	writeGoMod(t, root, ".")
	runner := &fakeRunner{output: gremlinsOutput(95, 95)}

	// A non-zero TestCPU is the trigger, and it is the schema
	// default — so the config that must NOT reach gremlins is
	// exactly the one every repo gets.
	cfg := Config{
		Packages: []Layer{{Path: "errs", Score: 90, Coverage: 90}},
		Gremlins: GremlinsConfig{Workers: 2, TestCPU: 2, TimeoutCoefficient: 30},
	}
	if err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg,
		nil, nil, RunOptions{}); err != nil {
		t.Fatalf("Run err: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("calls = %+v, want one", runner.calls)
	}
	if strings.Contains(runner.calls[0], "test-cpu") {
		t.Errorf("call = %q, must not carry --test-cpu: the flag makes gremlins "+
			"report every covered mutant KILLED regardless of test quality",
			runner.calls[0])
	}
	// The other two knobs must still reach the tool.
	for _, want := range []string{"--workers", "--timeout-coefficient"} {
		if !strings.Contains(runner.calls[0], want) {
			t.Errorf("call = %q, want it to carry %s", runner.calls[0], want)
		}
	}
}

// TestNoViableMutantsPasses covers a layer gremlins finds nothing to
// mutate in — single-expression generic wrappers with no branch or
// arithmetic to alter. gremlins emits no metrics for it, which is
// indistinguishable from an inadequate suite by metrics alone, so
// the "No results to report." signal is what separates the two.
func TestNoViableMutantsPasses(t *testing.T) {
	t.Parallel()

	t.Run("passes with a notice", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "pool")
		writeGoMod(t, root, ".")
		runner := &fakeRunner{
			output: "Gathering coverage... done in 30.02842ms\n\nNo results to report.\n",
		}

		var stdout bytes.Buffer
		cfg := Config{Packages: []Layer{{Path: "pool", Score: 99, Coverage: 99}}}
		if err := Run(t.Context(), runner, &stdout, io.Discard, root, cfg,
			nil, nil, RunOptions{}); err != nil {
			t.Fatalf("Run err = %v, want nil (nothing to mutate is not a failure)", err)
		}
		if !strings.Contains(stdout.String(), "no viable mutants") {
			t.Fatalf("stdout = %q, want a 'no viable mutants' notice", stdout.String())
		}
	})

	t.Run("passes even when gremlins exits non-zero", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "pool")
		writeGoMod(t, root, ".")
		// gremlins' exit code is not dependable in this state, so the
		// output signal is checked ahead of the error.
		runner := &fakeRunner{
			output: "No results to report.\n",
			runErr: errors.New("exit status 1"),
		}

		cfg := Config{Packages: []Layer{{Path: "pool", Score: 99, Coverage: 99}}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg,
			nil, nil, RunOptions{}); err != nil {
			t.Fatalf("Run err = %v, want nil", err)
		}
	})

	t.Run("a genuine failure with no metrics still fails", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "errs")
		writeGoMod(t, root, ".")
		runner := &fakeRunner{
			output: "ERROR: not in a Go module: open /go.mod: no such file or directory\n",
			runErr: errors.New("exit status 1"),
		}

		cfg := Config{Packages: []Layer{{Path: "errs", Score: 99, Coverage: 99}}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg,
			nil, nil, RunOptions{}); err == nil {
			t.Fatal("Run returned nil, want the gremlins failure surfaced")
		}
	})
}

// TestContributingFilesPrefix pins the path translation in the
// failing-target breakdown. gremlins reports files relative to its
// working directory, so the prefix must be the invoked module root —
// prefixing with the layer double-counts it when the two differ.
func TestContributingFilesPrefix(t *testing.T) {
	t.Parallel()
	root := buildTree(t, "errs")
	writeGoMod(t, root, ".")
	runner := &fakeRunner{output: strings.Join([]string{
		"Killed: 1, Lived: 1, Not covered: 0",
		"LIVED \"CONDITIONALS_BOUNDARY\" at errs/wrap.go:12:5",
		"Test efficacy: 50.00%",
		"Mutator coverage: 100.00%",
	}, "\n")}

	var stdout bytes.Buffer
	cfg := Config{Packages: []Layer{{Path: "errs", Score: 99, Coverage: 99}}}
	if err := Run(t.Context(), runner, &stdout, io.Discard, root, cfg,
		nil, nil, RunOptions{}); err == nil {
		t.Fatal("Run returned nil, want a below-threshold failure")
	}
	if strings.Contains(stdout.String(), "errs/errs/wrap.go") {
		t.Fatalf("stdout = %q, want no doubled layer prefix", stdout.String())
	}
	if !strings.Contains(stdout.String(), "errs/wrap.go") {
		t.Fatalf("stdout = %q, want the repo-relative path errs/wrap.go", stdout.String())
	}
}

// writeGoMod writes a minimal go.mod at dir (relative to root) so
// the module-root walk in resolveInvocation has something to find.
func writeGoMod(t *testing.T, root, dir string) {
	t.Helper()
	full := filepath.Join(root, dir)
	if err := os.MkdirAll(full, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	body := "module example.test/" + filepath.ToSlash(dir) + "\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(full, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatalf("write go.mod in %s: %v", full, err)
	}
}

// buildTree creates the named directories under a fresh tempdir
// and returns the root.
func buildTree(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	return root
}

// gremlinsOutput returns a synthetic gremlins log carrying the
// supplied score and coverage percentages plus the mutant counts
// the parser also extracts.
func gremlinsOutput(score, coverage int) string {
	return strings.Join([]string{
		"Killed: 50, Lived: 5, Not covered: 2",
		"Timed out: 0, Not viable: 0, Skipped: 0",
		"Test efficacy: " + strconv.Itoa(score) + "%",
		"Mutator coverage: " + strconv.Itoa(coverage) + "%",
	}, "\n") + "\n"
}

// TestExcludeRegexReachesGremlins covers the --exclude-files wiring:
// the shared checks.excludes / checks.skips policy is compiled into
// the single regex gremlins accepts, and the flag is omitted when
// the policy is empty.
func TestExcludeRegexReachesGremlins(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, excludes []policy.Exclude, skips []policy.Skip) string {
		t.Helper()
		root := buildTree(t, "errs")
		writeGoMod(t, root, ".")
		runner := &fakeRunner{output: gremlinsOutput(95, 95)}
		cfg := Config{
			Packages: []Layer{{Path: "errs", Score: 90, Coverage: 90}},
			Gremlins: GremlinsConfig{Workers: 2, TimeoutCoefficient: 30},
		}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg,
			excludes, skips, RunOptions{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		return runner.calls[0]
	}

	t.Run("an empty policy omits the flag", func(t *testing.T) {
		t.Parallel()
		if call := run(t, nil, nil); strings.Contains(call, "--exclude-files") {
			t.Errorf("call = %q, want no --exclude-files for an empty policy", call)
		}
	})

	t.Run("excludes and skips compile into the flag", func(t *testing.T) {
		t.Parallel()
		call := run(t,
			[]policy.Exclude{{Path: "**/*_test.go", Reason: "tests"}},
			[]policy.Skip{{Label: "gen", FuncGlob: "*", FileGlob: "**/zz_generated.go"}})
		if !strings.Contains(call, "--exclude-files") {
			t.Fatalf("call = %q, want --exclude-files", call)
		}
		if !strings.Contains(call, "_test") {
			t.Errorf("call = %q, want the exclude glob represented", call)
		}
	})
}

// TestVerboseMutantDump covers the --mutants report: a failing
// layer with more contributing files than the cap collapses the
// tail, and verbose appends the per-mutant locations.
func TestVerboseMutantDump(t *testing.T) {
	t.Parallel()

	// 12 contributing files against a cap of 10, all LIVED so the
	// layer fails and the diagnostic block renders.
	lines := []string{"Killed: 0, Lived: 12, Not covered: 0"}
	for i := range 12 {
		lines = append(lines,
			fmt.Sprintf("      LIVED CONDITIONALS_BOUNDARY at f%02d.go:%d:5", i, i+1))
	}
	lines = append(lines, "Test efficacy: 0.00%", "Mutator coverage: 100.00%")
	out := strings.Join(lines, "\n") + "\n"

	renderRun := func(t *testing.T, verbose bool) string {
		t.Helper()
		root := buildTree(t, "errs")
		writeGoMod(t, root, ".")
		runner := &fakeRunner{output: out}
		cfg := Config{
			Packages: []Layer{{Path: "errs", Score: 90, Coverage: 90}},
			Gremlins: GremlinsConfig{Workers: 2, TimeoutCoefficient: 30},
		}
		var stdout bytes.Buffer
		if err := Run(t.Context(), runner, &stdout, io.Discard, root, cfg,
			nil, nil, RunOptions{Verbose: verbose}); err == nil {
			t.Fatal("Run returned nil, want the below-threshold failure")
		}
		return stdout.String()
	}

	t.Run("caps the contributing-files list", func(t *testing.T) {
		t.Parallel()
		got := renderRun(t, false)
		if !strings.Contains(got, "more file(s)") {
			t.Errorf("stdout = %q, want the capped remainder reported", got)
		}
		if strings.Contains(got, "Non-killed mutants") {
			t.Errorf("stdout = %q, want no mutant dump without --mutants", got)
		}
	})

	t.Run("verbose appends the non-killed mutant locations", func(t *testing.T) {
		t.Parallel()
		got := renderRun(t, true)
		if !strings.Contains(got, "Non-killed mutants") {
			t.Errorf("stdout = %q, want the mutant dump", got)
		}
	})
}
