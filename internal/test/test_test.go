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
	"time"

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

		err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), Override{}, stage.Options{})
		if err != nil {
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
			"-timeout=10m0s", "./...",
		})
		cliCall, ok := byDir["/repo/cli"]
		if !ok {
			t.Fatalf("no call with dir /repo/cli in %+v", runner.calls)
		}

		// Asserted on the flag's presence and on where the profile is
		// NOT written, rather than on an exact path: the staging
		// directory is private to the run. Pinning a shared path here
		// would re-assert the defect — two concurrent runs aiming
		// `go test -coverprofile` at one file is what corrupted them.
		for _, c := range []recordedCall{rootCall, cliCall} {
			var profile string
			for _, a := range c.args {
				if after, found := strings.CutPrefix(a, "-coverprofile="); found {
					profile = after
				}
			}
			if profile == "" {
				t.Fatalf("args = %+v, want a -coverprofile flag", c.args)
			}
			if strings.HasPrefix(profile, in.CoverageDir) {
				t.Errorf("profile = %q, want it staged outside the published "+
					"directory %q", profile, in.CoverageDir)
			}
		}
	})

	t.Run("staged profiles are published into the coverage dir", func(t *testing.T) {
		t.Parallel()

		published := t.TempDir()
		runner := &fakeRunner{onRun: func(c recordedCall) {
			for _, a := range c.args {
				after, found := strings.CutPrefix(a, "-coverprofile=")
				if !found {
					continue
				}
				if err := os.WriteFile(after, []byte("mode: atomic\n"), 0o600); err != nil {
					t.Errorf("write staged profile: %v", err)
				}
			}
		}}
		in := Inputs{
			Root:        "/repo",
			Modules:     []modules.Module{{Dir: "."}, {Dir: "cli"}},
			CoverageDir: published,
		}

		if err := Run(t.Context(), runner, io.Discard, io.Discard,
			in, Defaults(), Override{}, stage.Options{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}

		// Staging is worthless if the gate cannot find the result:
		// `check coverage --no-test` reads exactly these paths.
		for _, name := range []string{"root.out", "cli.out"} {
			body, err := os.ReadFile(filepath.Join(published, name))
			if err != nil {
				t.Errorf("read published %s: %v", name, err)
				continue
			}
			if string(body) != "mode: atomic\n" {
				t.Errorf("%s = %q, want the staged contents", name, body)
			}
		}
	})

	t.Run("imports propagate as a -coverpkg flag", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{
			Root:    "/repo",
			Modules: []modules.Module{{Dir: "."}},
			Imports: []modules.Import{
				{Dir: ".", ImportPath: "go.example.com/proj"},
				{Dir: "cli", ImportPath: "go.example.com/proj/cli"},
			},
			CoverageDir: t.TempDir(),
		}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in,
			Defaults(), Override{}, stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		want := "-coverpkg=go.example.com/proj/...,go.example.com/proj/cli/..."
		if !slices.Contains(runner.calls[0].args, want) {
			t.Fatalf("args = %+v, want %q", runner.calls[0].args, want)
		}
	})

	t.Run("no imports omits -coverpkg (back to per-module-only)", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{
			Root: "/repo", Modules: []modules.Module{{Dir: "."}},
			CoverageDir: t.TempDir(),
		}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in,
			Defaults(), Override{}, stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		for _, a := range runner.calls[0].args {
			if strings.HasPrefix(a, "-coverpkg=") {
				t.Fatalf("unexpected -coverpkg flag with empty imports: %q", a)
			}
		}
	})

	t.Run("empty coverage dir disables coverage flag", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}

		err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), Override{}, stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		for _, a := range runner.calls[0].args {
			if strings.HasPrefix(a, "-coverprofile=") {
				t.Fatalf("unexpected coverprofile flag: %q", a)
			}
		}
	})
}

// TestCoverPkgArg pins the workspace-wide instrumentation arg
// builder: empty → empty (caller omits the flag); one or more
// imports → comma-separated `<ip>/...` wildcards.
func TestCoverPkgArg(t *testing.T) {
	t.Parallel()

	if got := coverPkgArg(nil); got != "" {
		t.Fatalf("empty imports → %q, want empty", got)
	}

	got := coverPkgArg([]modules.Import{
		{Dir: ".", ImportPath: "go.example.com/proj"},
		{Dir: "cli", ImportPath: "go.example.com/proj/cli"},
		{Dir: "backend", ImportPath: "go.example.com/proj/backend"},
	})
	want := "go.example.com/proj/...,go.example.com/proj/cli/...,go.example.com/proj/backend/..."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRunOverride pins the [Override] semantics on [Run]: zero
// fields keep the configured value, non-zero fields shadow it,
// and a non-empty Pattern adds `-run=<pattern>`.
func TestRunOverride(t *testing.T) {
	t.Parallel()

	t.Run("override count/cpu/timeout shadow the configured values", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}
		ov := Override{
			Count:   1,
			CPU:     2,
			Timeout: 30 * time.Second,
			Pattern: "TestFoo",
		}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), ov, stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		assertContainsAll(t, runner.calls[0].args, []string{
			"-count=1", "-cpu=2", "-timeout=30s", "-run=TestFoo",
		})
	})

	t.Run("zero-valued override keeps the configured defaults", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}
		err := Run(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			Defaults(),
			Override{},
			stage.Options{},
		)
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		assertContainsAll(t, runner.calls[0].args, []string{
			"-count=3", "-cpu=4", "-timeout=10m0s",
		})
		for _, a := range runner.calls[0].args {
			if strings.HasPrefix(a, "-run=") {
				t.Fatalf("unexpected -run flag with zero Pattern: %q", a)
			}
		}
	})
}

// TestBenchOverride pins the [Override] semantics on [Bench]:
// Pattern feeds `-bench`, Count maps to BenchCount, Time becomes
// `-benchtime`.
func TestBenchOverride(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}
	ov := Override{
		Pattern: "BenchmarkFoo",
		Count:   2,
		Time:    3 * time.Second,
	}
	err := Bench(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), ov, stage.Options{})
	if err != nil {
		t.Fatalf("Bench err: %v", err)
	}
	assertContainsAll(t, runner.calls[0].args, []string{
		"-bench=BenchmarkFoo", "-count=2", "-benchtime=3s",
	})
}

// TestRace pins the `go test -race` invocation shape.
func TestRace(t *testing.T) {
	t.Parallel()

	t.Run("emits go test -race with the race count per module", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "cli"}}}

		err := Race(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), Override{}, stage.Options{})
		if err != nil {
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

		err := Bench(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), Override{}, stage.Options{})
		if err != nil {
			t.Fatalf("Bench err: %v", err)
		}
		assertContainsAll(t, runner.calls[0].args, []string{
			"test", "-bench=.", "-run=^$", "-benchmem", "-timeout=10m0s", "./...",
		})
	})
}

// TestCoverage pins the per-module pipeline shape (`go tool cover
// -func` + `go tool cover -html`), the silent-skip behaviour for
// modules without a profile, and the rendered note that surfaces
// the total percentage + HTML filename.
func TestCoverage(t *testing.T) {
	t.Parallel()

	t.Run("runs -func + -html per profile and renders the % note", func(t *testing.T) {
		t.Parallel()
		coverDir := t.TempDir()
		// Only `cli` has a profile; root does not.
		if err := os.WriteFile(filepath.Join(coverDir, "cli.out"), []byte("mode: atomic\n"), 0o600); err != nil {
			t.Fatalf("write profile: %v", err)
		}
		runner := &fakeRunner{stdout: "go.example.com/x/pkg/foo.go:1:\tBar\t100.0%\ntotal:\t\t(statements)\t84.5%\n"}

		in := Inputs{
			Root:        "/repo",
			Modules:     []modules.Module{{Dir: "."}, {Dir: "cli"}},
			CoverageDir: coverDir,
		}
		var stdout strings.Builder
		if err := Coverage(t.Context(), runner, &stdout, io.Discard, in, stage.Options{}); err != nil {
			t.Fatalf("Coverage err: %v", err)
		}
		// Two calls per profile (-func then -html); root has no
		// profile and is skipped.
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(runner.calls))
		}
		// The profile is read from the published directory; the HTML
		// is written to a staging directory and published afterwards,
		// so -o must NOT name coverDir. `go tool cover -o` truncates
		// its target before filling it, and pinning the shared path
		// here would re-assert the defect: a concurrent reader saw a
		// blank report.
		assertContainsAll(t, runner.calls[1].args, []string{
			"tool", "cover",
			"-html=" + filepath.Join(coverDir, "cli.out"),
			"-o",
		})
		oi := slices.Index(runner.calls[1].args, "-o")
		if oi < 0 || oi+1 >= len(runner.calls[1].args) {
			t.Fatalf("args = %+v, want -o <path>", runner.calls[1].args)
		}
		if out := runner.calls[1].args[oi+1]; strings.HasPrefix(out, coverDir) {
			t.Errorf("-o = %q, want it staged outside the published directory %q",
				out, coverDir)
		}
		if !strings.Contains(stdout.String(), "84.5%") {
			t.Fatalf("stdout missing parsed percent: %q", stdout.String())
		}
		// The rendered note should carry both the .out and .html
		// paths relative to the repo root, joined by an arrow. The
		// .html path named is the published one — the staging path is
		// gone by the time the reader could open it.
		if !strings.Contains(stdout.String(), "cli.out") || !strings.Contains(stdout.String(), "cli.html") {
			t.Fatalf("stdout missing out/html pair: %q", stdout.String())
		}
		if !strings.Contains(stdout.String(), "→") {
			t.Fatalf("stdout missing arrow separator: %q", stdout.String())
		}
	})

	t.Run("missing coverage dir surfaces an error", func(t *testing.T) {
		t.Parallel()
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}
		if err := Coverage(t.Context(), &fakeRunner{}, io.Discard, io.Discard, in, stage.Options{}); err == nil {
			t.Fatal("Coverage returned nil for empty CoverageDir")
		}
	})

	t.Run("-func failure surfaces with the captured output", func(t *testing.T) {
		t.Parallel()
		coverDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(coverDir, "cli.out"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write profile: %v", err)
		}
		runner := &fakeRunner{runErr: errors.New("malformed profile"), stdout: "boom\n"}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "cli"}}, CoverageDir: coverDir}
		err := Coverage(t.Context(), runner, io.Discard, io.Discard, in, stage.Options{})
		if err == nil {
			t.Fatal("Coverage returned nil, want subprocess error")
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
		if err := Fuzz(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), Override{}); err != nil {
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
		if err := Fuzz(t.Context(), runner, &stdout, io.Discard, in, Defaults(), Override{}); err != nil {
			t.Fatalf("Fuzz err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %+v, want zero", runner.calls)
		}
		if !strings.Contains(stdout.String(), "no fuzz targets") {
			t.Fatalf("stdout = %q, want note about missing targets", stdout.String())
		}
	})

	t.Run("ov.Pattern filters discovered targets", func(t *testing.T) {
		t.Parallel()
		root := buildFuzzTree(t, map[string]string{
			"pkg/foo_test.go": fuzzSource("FuzzFoo"),
			"pkg/bar_test.go": fuzzSource("FuzzBar"),
		})
		runner := &fakeRunner{}
		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		if err := Fuzz(t.Context(), runner, io.Discard, io.Discard, in,
			Defaults(), Override{Pattern: "Foo"}); err != nil {
			t.Fatalf("Fuzz err: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1 (only FuzzFoo)", len(runner.calls))
		}
		if !strings.Contains(strings.Join(runner.calls[0].args, " "), "FuzzFoo") {
			t.Fatalf("args = %+v, want -fuzz=^FuzzFoo$", runner.calls[0].args)
		}
	})

	t.Run("invalid ov.Pattern is a regex compile error", func(t *testing.T) {
		t.Parallel()
		root := buildFuzzTree(t, map[string]string{
			"pkg/foo_test.go": fuzzSource("FuzzFoo"),
		})
		runner := &fakeRunner{}
		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		err := Fuzz(t.Context(), runner, io.Discard, io.Discard, in,
			Defaults(), Override{Pattern: "[unterminated"})
		if err == nil {
			t.Fatal("Fuzz returned nil, want regex error")
		}
	})

	t.Run("ov.Timeout passes through as -timeout", func(t *testing.T) {
		t.Parallel()
		root := buildFuzzTree(t, map[string]string{
			"pkg/foo_test.go": fuzzSource("FuzzFoo"),
		})
		runner := &fakeRunner{}
		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		if err := Fuzz(t.Context(), runner, io.Discard, io.Discard, in,
			Defaults(), Override{Timeout: time.Minute}); err != nil {
			t.Fatalf("Fuzz err: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(runner.calls))
		}
		joined := strings.Join(runner.calls[0].args, " ")
		if !strings.Contains(joined, "-timeout=") {
			t.Fatalf("args = %+v, want -timeout flag", runner.calls[0].args)
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
		err := Fuzz(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), Override{})
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
// default (parallel) fan-out. stdout, when set, is written to
// opts.Stdout on every invocation; runErr is the simulated
// subprocess exit.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []recordedCall
	stdout string
	runErr error

	// onRun, when set, runs for each invocation with the recorded
	// call. Lets a test stand in for the side effects of the real
	// binary — `go test -coverprofile=<p>` writing <p> — which is
	// the only way to observe what the runner does with a file it
	// was told to produce. Called outside the lock, so it may
	// itself touch the filesystem.
	onRun func(recordedCall)
}

type recordedCall struct {
	dir  string
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	call := recordedCall{dir: filepath.ToSlash(opts.Dir), name: name, args: slices.Clone(args)}
	f.mu.Lock()
	// filepath.ToSlash normalises Windows backslashes so the
	// per-dir assertions stay portable across operating systems.
	f.calls = append(f.calls, call)
	onRun := f.onRun
	f.mu.Unlock()
	if onRun != nil {
		onRun(call)
	}
	if opts.Stdout != nil && f.stdout != "" {
		_, _ = opts.Stdout.Write([]byte(f.stdout))
	}
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
