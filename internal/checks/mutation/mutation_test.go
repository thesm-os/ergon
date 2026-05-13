// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package mutation

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
		root := buildTree(t, "core")
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
		if !strings.Contains(runner.calls[0], "./kernel/fold/") {
			t.Fatalf("call = %q, want subpath './kernel/fold/'", runner.calls[0])
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

	t.Run("bare-layer argument selects the layer with RelPath '.'", func(t *testing.T) {
		t.Parallel()
		got, err := selectTargets(packages, []string{"core"})
		if err != nil {
			t.Fatalf("selectTargets err: %v", err)
		}
		if len(got) != 1 || got[0].Layer != "core" || got[0].RelPath != "." || got[0].Label != "core" {
			t.Fatalf("target = %+v, want core whole-layer", got)
		}
	})

	t.Run("layer/subpath sets RelPath and Label", func(t *testing.T) {
		t.Parallel()
		got, err := selectTargets(packages, []string{"core/kernel/fold"})
		if err != nil {
			t.Fatalf("selectTargets err: %v", err)
		}
		if got[0].RelPath != "./kernel/fold/" || got[0].Label != "core/kernel/fold" {
			t.Fatalf("target = %+v, want RelPath=./kernel/fold/ Label=core/kernel/fold", got[0])
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

	cases := []struct {
		in    string
		layer string
		sub   string
	}{
		{"foundation", "foundation", ""},
		{"core/kernel/fold", "core", "kernel/fold"},
		{"core/kernel/fold/", "core", "kernel/fold"},
		{"", "", ""},
	}
	for _, tc := range cases {
		gotLayer, gotSub := splitLayerSubpath(tc.in)
		if gotLayer != tc.layer || gotSub != tc.sub {
			t.Errorf("splitLayerSubpath(%q) = (%q, %q), want (%q, %q)",
				tc.in, gotLayer, gotSub, tc.layer, tc.sub)
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
type fakeRunner struct {
	calls  []string
	output string
	runErr error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	// filepath.ToSlash normalises Windows backslashes so the call
	// comparisons stay portable across operating systems.
	f.calls = append(f.calls, filepath.ToSlash(opts.Dir)+": "+name+" "+strings.Join(args, " "))
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(f.output))
	}
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
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
