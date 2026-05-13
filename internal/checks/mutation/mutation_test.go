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

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// TestParsePercent pins the percentage extractor against gremlins'
// canonical output lines.
func TestParsePercent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		out    string
		prefix string
		want   int
	}{
		{"Test efficacy: 87.5%\n", "Test efficacy:", 87},
		{"Mutator coverage: 92%\n", "Mutator coverage:", 92},
		{"prefix not present", "Test efficacy:", 0},
		{"Test efficacy: garbage%\n", "Test efficacy:", 0},
	}
	for _, tc := range cases {
		got := parsePercent(tc.out, tc.prefix)
		if got != tc.want {
			t.Errorf("parsePercent(%q, %q) = %d, want %d", tc.out, tc.prefix, got, tc.want)
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

// TestRun pins the high-level contract: iterates layers, runs
// gremlins per layer, applies both thresholds, fails when either
// gate misses, short-circuits on empty Packages, skips missing
// directories with a notice.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("empty packages short-circuit with a notice", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		var stdout strings.Builder
		if err := Run(t.Context(), runner, &stdout, io.Discard, t.TempDir(), Config{}); err != nil {
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
		if err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
	})

	t.Run("score below threshold fails", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{output: gremlinsOutput(70, 95)}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90, Coverage: 90}}}
		var stderr strings.Builder
		err := Run(t.Context(), runner, io.Discard, &stderr, root, cfg)
		if err == nil {
			t.Fatal("Run returned nil, want failure")
		}
		if !strings.Contains(stderr.String(), "foundation") {
			t.Fatalf("stderr = %q, want failure list to name the layer", stderr.String())
		}
	})

	t.Run("coverage below threshold fails", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{output: gremlinsOutput(95, 50)}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90, Coverage: 90}}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg); err == nil {
			t.Fatal("Run returned nil, want failure")
		}
	})

	t.Run("omitted coverage defaults to score", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{output: gremlinsOutput(95, 91)}

		// Coverage left at zero — the runtime defaults it to Score (90).
		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90}}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg); err != nil {
			t.Fatalf("Run err: %v, want score-default to accept coverage=91", err)
		}
	})

	t.Run("missing directory skips the layer with a notice", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir() // no `foundation` dir
		runner := &fakeRunner{}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90}}}
		var stdout strings.Builder
		if err := Run(t.Context(), runner, &stdout, io.Discard, root, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %+v, want zero (dir missing)", runner.calls)
		}
		if !strings.Contains(stdout.String(), "skip — directory missing") {
			t.Fatalf("stdout = %q, want skip notice", stdout.String())
		}
	})

	t.Run("gremlins with no metrics surfaces an error", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "foundation")
		runner := &fakeRunner{output: "gremlins started... no test files\n", runErr: errors.New("exit 1")}

		cfg := Config{Packages: []Layer{{Path: "foundation/...", Score: 90}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root, cfg)
		if err == nil {
			t.Fatal("Run returned nil, want gremlins error")
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. It records each
// Run invocation, writes `output` to opts.Stdout, and returns
// runErr from the call.
type fakeRunner struct {
	calls  []string
	output string
	runErr error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.calls = append(f.calls, opts.Dir+": "+name+" "+strings.Join(args, " "))
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
// the bash script's parser also extracts (kept for fidelity even
// though the Go port ignores them today).
func gremlinsOutput(score, coverage int) string {
	return strings.Join([]string{
		"Killed: 50, Lived: 5, Not covered: 2",
		"Timed out: 0, Not viable: 0, Skipped: 0",
		"Test efficacy: " + itoa(score) + "%",
		"Mutator coverage: " + itoa(coverage) + "%",
	}, "\n") + "\n"
}

// itoa is a tiny alias kept so [gremlinsOutput] reads as a series
// of percentage assignments rather than nested strconv calls.
func itoa(n int) string {
	return strconv.Itoa(n)
}
