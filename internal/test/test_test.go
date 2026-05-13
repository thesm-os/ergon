// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// TestRun pins the `go test ./...` invocation shape: standard
// knobs (cpu, count, timeout) plus coverage profile per module
// when in.CoverageDir is set.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("emits go test with the configured knobs per module", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{
			Root:        "/repo",
			Modules:     []modules.Module{{Dir: "."}, {Dir: "cli"}},
			CoverageDir: t.TempDir(),
		}

		if err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), stage.Options{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(runner.calls))
		}
		// Parallel execution makes the recording order
		// non-deterministic; index calls by dir before asserting.
		byDir := map[string]recordedCall{}
		for _, c := range runner.calls {
			byDir[c.dir] = c
		}
		rootCall, ok := byDir["/repo"]
		if !ok {
			t.Fatalf("no call with dir /repo in %+v", runner.calls)
		}
		assertContainsAll(t, rootCall.args, []string{
			"test", "-covermode=atomic", "-cpu=4", "-count=3",
			"-timeout=10m0s",
			"-coverprofile=" + filepath.Join(in.CoverageDir, "root.out"),
			"./...",
		})
		cliCall, ok := byDir["/repo/cli"]
		if !ok {
			t.Fatalf("no call with dir /repo/cli in %+v", runner.calls)
		}
		wantFlag := "-coverprofile=" + filepath.Join(in.CoverageDir, "cli.out")
		if !slices.Contains(cliCall.args, wantFlag) {
			t.Fatalf("cli args = %+v, want %q", cliCall.args, wantFlag)
		}
	})

	t.Run("empty coverage dir disables coverage flag", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}

		if err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), stage.Options{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		for _, a := range runner.calls[0].args {
			if strings.HasPrefix(a, "-coverprofile=") {
				t.Fatalf("unexpected coverprofile flag: %q", a)
			}
		}
	})
}

// TestRace pins the `go test -race` invocation shape.
func TestRace(t *testing.T) {
	t.Parallel()

	t.Run("emits go test -race with the race count per module", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "cli"}}}

		if err := Race(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), stage.Options{}); err != nil {
			t.Fatalf("Race err: %v", err)
		}
		assertContainsAll(t, runner.calls[0].args, []string{
			"test", "-race", "-count=3", "-timeout=10m0s", "./...",
		})
	})
}

// TestBench pins the `go test -bench` invocation shape.
func TestBench(t *testing.T) {
	t.Parallel()

	t.Run("emits go test -bench with run=^$ to skip regular tests", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}

		if err := Bench(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), stage.Options{}); err != nil {
			t.Fatalf("Bench err: %v", err)
		}
		assertContainsAll(t, runner.calls[0].args, []string{
			"test", "-bench=.", "-run=^$", "-benchmem", "-timeout=10m0s", "./...",
		})
	})
}

// TestCoverage pins the `go tool cover -html` invocation shape and
// the silent-skip behaviour for modules without a profile.
func TestCoverage(t *testing.T) {
	t.Parallel()

	t.Run("converts existing profiles to html and skips missing ones", func(t *testing.T) {
		t.Parallel()
		coverDir := t.TempDir()
		// Only `cli` has a profile; root does not.
		if err := os.WriteFile(filepath.Join(coverDir, "cli.out"), []byte("mode: atomic\n"), 0o600); err != nil {
			t.Fatalf("write profile: %v", err)
		}
		runner := &fakeRunner{}
		in := Inputs{
			Root:        "/repo",
			Modules:     []modules.Module{{Dir: "."}, {Dir: "cli"}},
			CoverageDir: coverDir,
		}

		if err := Coverage(t.Context(), runner, io.Discard, io.Discard, in); err != nil {
			t.Fatalf("Coverage err: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %+v, want one (cli only)", runner.calls)
		}
		assertContainsAll(t, runner.calls[0].args, []string{
			"tool", "cover",
			"-html=" + filepath.Join(coverDir, "cli.out"),
			"-o", filepath.Join(coverDir, "cli.html"),
		})
	})

	t.Run("missing coverage dir surfaces an error", func(t *testing.T) {
		t.Parallel()
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}
		if err := Coverage(t.Context(), &fakeRunner{}, io.Discard, io.Discard, in); err == nil {
			t.Fatal("Coverage returned nil for empty CoverageDir")
		}
	})
}

// TestFuzz combines [DiscoverFuzzTargets] (real filesystem walk
// over a tempdir of test files) with the per-target run shape.
func TestFuzz(t *testing.T) {
	t.Parallel()

	t.Run("runs every discovered Fuzz target once", func(t *testing.T) {
		t.Parallel()
		root := buildFuzzTree(t, map[string]string{
			"pkg/foo_test.go": fuzzSource("FuzzFoo"),
			"pkg/bar_test.go": fuzzSource("FuzzBar"),
		})
		runner := &fakeRunner{}

		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		if err := Fuzz(t.Context(), runner, io.Discard, io.Discard, in, Defaults()); err != nil {
			t.Fatalf("Fuzz err: %v", err)
		}
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(runner.calls))
		}
		gotFuzzFlags := []string{
			fuzzFlag(t, runner.calls[0].args),
			fuzzFlag(t, runner.calls[1].args),
		}
		want := []string{"-fuzz=^FuzzBar$", "-fuzz=^FuzzFoo$"}
		slices.Sort(gotFuzzFlags)
		slices.Sort(want)
		if !slices.Equal(gotFuzzFlags, want) {
			t.Fatalf("fuzz flags = %+v, want %+v", gotFuzzFlags, want)
		}
	})

	t.Run("no fuzz targets returns nil with a stdout note", func(t *testing.T) {
		t.Parallel()
		root := buildFuzzTree(t, nil)
		runner := &fakeRunner{}

		var stdout strings.Builder
		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		if err := Fuzz(t.Context(), runner, &stdout, io.Discard, in, Defaults()); err != nil {
			t.Fatalf("Fuzz err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %+v, want zero", runner.calls)
		}
		if !strings.Contains(stdout.String(), "no fuzz targets") {
			t.Fatalf("stdout = %q, want note about missing targets", stdout.String())
		}
	})

	t.Run("first-target failure short-circuits remaining targets", func(t *testing.T) {
		t.Parallel()
		root := buildFuzzTree(t, map[string]string{
			"a/a_test.go": fuzzSource("FuzzA"),
			"b/b_test.go": fuzzSource("FuzzB"),
		})
		runner := &fakeRunner{runErr: errors.New("crash")}

		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		err := Fuzz(t.Context(), runner, io.Discard, io.Discard, in, Defaults())
		if err == nil {
			t.Fatal("Fuzz returned nil, want error")
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1 (short-circuit)", len(runner.calls))
		}
	})
}

// TestDiscoverFuzzTargets pins the walker's contract: discovers
// Fuzz* funcs in `_test.go` files, skips `testdata/`, `.git/`,
// `vendor/`, and reports the package-relative path the runner uses.
func TestDiscoverFuzzTargets(t *testing.T) {
	t.Parallel()

	t.Run("finds Fuzz funcs and resolves package paths relative to module", func(t *testing.T) {
		t.Parallel()
		root := buildFuzzTree(t, map[string]string{
			"root_test.go":        fuzzSource("FuzzRoot"),
			"pkg/sub_test.go":     fuzzSource("FuzzSub"),
			"pkg/nope_test.go":    "package pkg\n",
			"pkg/regular_test.go": "package pkg\n\nfunc TestRegular(t *testing.T) {}\n",
		})

		got, err := DiscoverFuzzTargets(root, []modules.Module{{Dir: "."}})
		if err != nil {
			t.Fatalf("DiscoverFuzzTargets err: %v", err)
		}
		seen := map[string]string{}
		for _, target := range got {
			seen[target.Name] = target.PkgRel
		}
		if seen["FuzzRoot"] != "./" {
			t.Fatalf("FuzzRoot pkgRel = %q, want ./", seen["FuzzRoot"])
		}
		if seen["FuzzSub"] != "./pkg" {
			t.Fatalf("FuzzSub pkgRel = %q, want ./pkg", seen["FuzzSub"])
		}
		if _, ok := seen["FuzzNope"]; ok {
			t.Fatal("unexpected FuzzNope (no such target)")
		}
	})

	t.Run("skips testdata subtrees", func(t *testing.T) {
		t.Parallel()
		root := buildFuzzTree(t, map[string]string{
			"testdata/should_not_appear_test.go": fuzzSource("FuzzShouldNotAppear"),
		})

		got, err := DiscoverFuzzTargets(root, []modules.Module{{Dir: "."}})
		if err != nil {
			t.Fatalf("DiscoverFuzzTargets err: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("targets = %+v, want zero (testdata skipped)", got)
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. The mutex
// covers calls so the runner is safe under stage.PerModule's
// default (parallel) fan-out.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []recordedCall
	runErr error
}

type recordedCall struct {
	dir  string
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{dir: opts.Dir, name: name, args: slices.Clone(args)})
	f.mu.Unlock()
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// buildFuzzTree writes the given file map into a fresh tempdir
// (paths relative to the tempdir root) and returns the absolute
// root path. A nil map produces an empty repo.
func buildFuzzTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}

// fuzzSource returns a minimal Go test source containing one
// `func <name>(f *testing.F)` declaration.
func fuzzSource(name string) string {
	return "package x\n\nimport \"testing\"\n\nfunc " + name + "(f *testing.F) {}\n"
}

// fuzzFlag pulls the `-fuzz=^Name$` argument out of a recorded
// `go test` call and fails the test when absent.
func fuzzFlag(t *testing.T, args []string) string {
	t.Helper()
	for _, a := range args {
		if strings.HasPrefix(a, "-fuzz=") {
			return a
		}
	}
	t.Fatalf("no -fuzz flag in %+v", args)
	return ""
}

// assertContainsAll fails the test when any expected arg is not
// present in got. Order is not enforced; callers that need order
// inspect got directly.
func assertContainsAll(t *testing.T, got, want []string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Fatalf("args = %+v, want it to contain %q", got, w)
		}
	}
}
