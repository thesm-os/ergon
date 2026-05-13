// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/test"
)

// TestBaseline pins the contract of [Baseline]: writes the baseline
// file only when `Benchmark` lines are present in the captured
// output, surfaces subprocess failures with the module dir, and
// creates the destination directory as needed.
func TestBaseline(t *testing.T) {
	t.Parallel()

	t.Run("writes per-module bench output to the baseline path", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{outputs: map[string]string{
			"go": "BenchmarkX-8\t1000\t100 ns/op\n",
		}}

		err := Baseline(t.Context(), runner, io.Discard, io.Discard,
			root,
			[]modules.Module{{Dir: "."}, {Dir: "cli"}},
			testCfg(6, 10*time.Minute),
			Defaults())
		if err != nil {
			t.Fatalf("Baseline err: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(root, "bench", "baseline.txt"))
		if err != nil {
			t.Fatalf("read baseline: %v", err)
		}
		if strings.Count(string(body), "BenchmarkX-8") != 2 {
			t.Fatalf("baseline = %q, want both modules' output", string(body))
		}
	})

	t.Run("no benchmarks short-circuits and does not write the file", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{outputs: map[string]string{
			"go": "PASS\nok  pkg/foo  0.001s\n",
		}}

		err := Baseline(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "."}}, testCfg(1, time.Minute), Defaults())
		if err != nil {
			t.Fatalf("Baseline err: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "bench", "baseline.txt")); !os.IsNotExist(err) {
			t.Fatalf("baseline written despite no benchmarks; stat err = %v", err)
		}
	})

	t.Run("creates the baseline directory when missing", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{outputs: map[string]string{
			"go": "BenchmarkX-8\t1\t1 ns/op\n",
		}}

		if err := Baseline(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "."}}, testCfg(1, time.Minute), Defaults()); err != nil {
			t.Fatalf("Baseline err: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "bench", "baseline.txt")); err != nil {
			t.Fatalf("baseline file missing: %v", err)
		}
	})

	t.Run("subprocess failure surfaces the module dir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{runErr: errors.New("bench failed")}

		err := Baseline(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "cli"}}, testCfg(1, time.Minute), Defaults())
		if err == nil {
			t.Fatal("Baseline returned nil, want error")
		}
		if !strings.Contains(err.Error(), "[cli]") {
			t.Fatalf("err = %v, want [cli]", err)
		}
	})
}

// TestRegression pins the contract of [Regression]: requires the
// baseline file to exist, runs the bench into a temp file, hands
// both to benchstat (text + CSV), parses the CSV, and enforces
// per-metric thresholds.
func TestRegression(t *testing.T) {
	t.Parallel()

	t.Run("missing baseline file surfaces ErrBaselineMissing", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{}

		err := Regression(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "."}}, testCfg(1, time.Minute), Defaults())
		if !errors.Is(err, ErrBaselineMissing) {
			t.Fatalf("err = %v, want ErrBaselineMissing", err)
		}
	})

	t.Run("no benchmarks short-circuits with no regression check", func(t *testing.T) {
		t.Parallel()
		root := seededRoot(t)
		runner := &fakeRunner{outputs: map[string]string{
			"go": "PASS\nok\n",
		}}

		err := Regression(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "."}}, testCfg(1, time.Minute), Defaults())
		if err != nil {
			t.Fatalf("Regression err: %v", err)
		}
		// Only the bench call happened; benchstat never ran.
		for _, c := range runner.calls {
			if c.name == "benchstat" {
				t.Fatalf("benchstat ran despite no benchmarks: %+v", c)
			}
		}
	})

	t.Run("happy path: bench + benchstat (text + csv); zero regressions returns nil", func(t *testing.T) {
		t.Parallel()
		root := seededRoot(t)
		runner := &fakeRunner{outputs: map[string]string{
			"go":            "BenchmarkX-8\t1\t1 ns/op\n",
			"benchstat":     "no regressions in human form\n",
			"benchstat-csv": noRegressionCSV,
		}}

		err := Regression(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "."}}, testCfg(1, time.Minute), Defaults())
		if err != nil {
			t.Fatalf("Regression err: %v", err)
		}
		// Three calls: go test, benchstat (text), benchstat -format csv.
		if len(runner.calls) != 3 {
			t.Fatalf("calls = %d, want 3 (%+v)", len(runner.calls), callNames(runner.calls))
		}
		want := []string{"go", "benchstat", "benchstat"}
		if !slices.Equal(callNames(runner.calls), want) {
			t.Fatalf("call sequence = %+v, want %+v", callNames(runner.calls), want)
		}
	})

	t.Run("regressions exceeding threshold surface as an error", func(t *testing.T) {
		t.Parallel()
		root := seededRoot(t)
		runner := &fakeRunner{outputs: map[string]string{
			"go":            "BenchmarkX-8\t1\t1 ns/op\n",
			"benchstat":     "regressions in human form\n",
			"benchstat-csv": sampleCSV, // +8.9% / +50% deltas vs 5% threshold
		}}

		err := Regression(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "."}}, testCfg(1, time.Minute), Defaults())
		if err == nil {
			t.Fatal("Regression returned nil, want regression error")
		}
		if !strings.Contains(err.Error(), "regression") {
			t.Fatalf("err = %v, want it to mention regression", err)
		}
	})
}

// noRegressionCSV is `benchstat -format csv` output where old and
// new values are identical so every metric yields delta=0.
const noRegressionCSV = `goos: linux
goarch: amd64
pkg: foo
cpu: Intel
,/old.txt,,/new.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
A-8,100,∞,100,∞,~,p=1.000 n=3
geomean,100,,100,,+0.00%,
`

// fakeRunner satisfies [xexec.Runner] for tests. outputs is keyed
// by command name (e.g. "go", "benchstat"); a "benchstat-csv"
// entry overrides "benchstat" when the args contain "csv" so
// tests can supply distinct stdout for the text and CSV
// invocations.
type fakeRunner struct {
	calls   []recordedCall
	outputs map[string]string
	runErr  error
}

type recordedCall struct {
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.calls = append(f.calls, recordedCall{name: name, args: slices.Clone(args)})
	if opts.Stdout != nil {
		key := name
		if name == "benchstat" && slices.Contains(args, "csv") {
			key = "benchstat-csv"
		}
		if out, ok := f.outputs[key]; ok {
			_, _ = opts.Stdout.Write([]byte(out))
		}
	}
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// seededRoot returns a fresh tempdir with bench/baseline.txt
// populated so [Regression] does not short-circuit on
// ErrBaselineMissing.
func seededRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bench"), 0o700); err != nil {
		t.Fatalf("mkdir bench: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bench", "baseline.txt"),
		[]byte("BenchmarkX-8\t1\t1 ns/op\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return root
}

// callNames extracts the command names from a recorded call
// sequence so tests can assert on order without per-arg noise.
func callNames(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.name)
	}
	return out
}

// testCfg returns a test.Config with the supplied bench-relevant
// knobs set; other fields stay at their zero value because
// [runBench] only reads BenchCount and Timeout.
func testCfg(benchCount int, timeout time.Duration) test.Config {
	return test.Config{
		BenchCount: benchCount,
		Timeout:    timeout,
	}
}
