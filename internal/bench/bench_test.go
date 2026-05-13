// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/test"
)

// TestBaseline pins the contract of [Baseline]: runs the bench
// invocation per module, concatenates output into cfg.BaselinePath,
// and creates the containing directory when missing.
func TestBaseline(t *testing.T) {
	t.Parallel()

	t.Run("writes per-module bench output to the baseline path", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{output: "BenchmarkX-8\t1000\t100 ns/op\n"}

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
		// Both modules' output should be present (the fake runner
		// produces the same line twice — once per module).
		if strings.Count(string(body), "BenchmarkX-8") != 2 {
			t.Fatalf("baseline = %q, want both modules' output", string(body))
		}
	})

	t.Run("creates the baseline directory when missing", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{output: "BenchmarkX-8\t1\t1 ns/op\n"}

		// bench/ does not exist yet.
		err := Baseline(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "."}}, testCfg(1, time.Minute), Defaults())
		if err != nil {
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
// baseline file to exist, runs the bench into a temp file, and
// invokes benchstat against the (baseline, current) pair.
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

	t.Run("runs bench then invokes benchstat with both paths", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		baselineDir := filepath.Join(root, "bench")
		if err := os.MkdirAll(baselineDir, 0o700); err != nil {
			t.Fatalf("mkdir bench: %v", err)
		}
		if err := os.WriteFile(filepath.Join(baselineDir, "baseline.txt"),
			[]byte("BenchmarkX-8\t1\t1 ns/op\n"), 0o600); err != nil {
			t.Fatalf("write baseline: %v", err)
		}

		runner := &recordingRunner{output: "BenchmarkX-8\t1\t2 ns/op\n"}
		err := Regression(t.Context(), runner, io.Discard, io.Discard,
			root, []modules.Module{{Dir: "."}}, testCfg(1, time.Minute), Defaults())
		if err != nil {
			t.Fatalf("Regression err: %v", err)
		}
		// First call is go test -bench; second is benchstat.
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(runner.calls))
		}
		if runner.calls[1].name != "benchstat" {
			t.Fatalf("calls[1].name = %q, want benchstat", runner.calls[1].name)
		}
		if !strings.Contains(runner.calls[1].args[0], "baseline.txt") {
			t.Fatalf("benchstat args = %+v, want baseline.txt as first arg", runner.calls[1].args)
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. It writes
// `output` to opts.Stdout for every Run invocation and returns
// runErr.
type fakeRunner struct {
	output string
	runErr error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(f.output))
	}
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// recordingRunner is fakeRunner with per-call recording so tests
// can assert on the sequence.
type recordingRunner struct {
	calls  []recordedCall
	output string
}

type recordedCall struct {
	name string
	args []string
}

func (r *recordingRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(r.output))
	}
	r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...)})
	return nil
}

func (*recordingRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
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
