// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package lint

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

	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// TestVet pins the per-module `go vet ./...` shape.
func TestVet(t *testing.T) {
	t.Parallel()

	t.Run("runs go vet ./... per module in declared order", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}, {Dir: "cli"}}}

		if err := Vet(t.Context(), runner, io.Discard, io.Discard, in, stage.Options{}); err != nil {
			t.Fatalf("Vet err: %v", err)
		}
		want := []string{
			"/repo: go vet ./...",
			"/repo/cli: go vet ./...",
		}
		assertCalls(t, runner.calls, want)
	})

	t.Run("vet failure surfaces the module dir", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("vet finding")}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "cli"}}}

		err := Vet(t.Context(), runner, io.Discard, io.Discard, in, stage.Options{})
		if err == nil {
			t.Fatal("Vet returned nil, want error")
		}
		if !strings.Contains(err.Error(), "[cli]") {
			t.Fatalf("err = %v, want it to mention [cli]", err)
		}
	})

	t.Run("default mode runs every module even when one fails", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("vet finding")}
		in := Inputs{
			Root:    "/repo",
			Modules: []modules.Module{{Dir: "."}, {Dir: "cli"}, {Dir: "api"}},
		}

		err := Vet(t.Context(), runner, io.Discard, io.Discard, in, stage.Options{})
		if err == nil {
			t.Fatal("Vet returned nil, want aggregated error")
		}
		if len(runner.calls) != 3 {
			t.Fatalf("calls = %d, want 3 (every module ran in default mode)", len(runner.calls))
		}
	})

	t.Run("fast mode aborts at the first failure", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("vet finding")}
		in := Inputs{
			Root:    "/repo",
			Modules: []modules.Module{{Dir: "."}, {Dir: "cli"}, {Dir: "api"}},
		}

		err := Vet(t.Context(), runner, io.Discard, io.Discard, in, stage.Options{Fast: true})
		if err == nil {
			t.Fatal("Vet returned nil, want error")
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1 (fast mode aborts at first failure)", len(runner.calls))
		}
	})
}

// TestGo pins the per-module `golangci-lint run` shape — no
// timeout flag is passed, golangci-lint reads its own config.
func TestGo(t *testing.T) {
	t.Parallel()

	t.Run("runs golangci-lint per module without ergon-specific flags", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		in := Inputs{Root: "/repo", Modules: []modules.Module{{Dir: "."}}}

		if err := Go(t.Context(), runner, io.Discard, io.Discard, in, stage.Options{}); err != nil {
			t.Fatalf("Go err: %v", err)
		}
		assertCalls(t, runner.calls, []string{"/repo: golangci-lint run ./..."})
	})
}

// TestAll pins the umbrella's contract:
//
//   - Default mode runs the full stage list (vet, go, md, license,
//     skip-expiry, error-prefix, vuln) in declared order; each
//     subprocess-backed stage records its canonical invocation.
//   - Fast mode aborts at the first failing stage.
//   - The Filter (config + CLI overrides) narrows the live stage
//     set and Unknown stage names surface [stage.ErrUnknownStage].
//
// skip-expiry and error-prefix do not record subprocess calls
// (pure AST scans), so they verify implicitly via the absence of
// orchestrator failures. The stages' internal contracts have
// their own coverage in `internal/checks/skipexpiry` and
// `internal/checks/errorprefix`.
func TestAll(t *testing.T) {
	t.Parallel()

	t.Run("orchestrates every static-analysis stage in declared order", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{
			Root:     root,
			Modules:  []modules.Module{{Dir: "."}},
			GitFiles: stubGitFiles(),
		}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			errorprefix.Defaults(),
			stage.Filter{},
			stage.Options{},
		)
		if err != nil {
			t.Fatalf("All err: %v", err)
		}
		// Subprocess-backed stages, in declared order. skip-expiry
		// and error-prefix are AST scans — they don't shell out
		// and therefore don't appear here.
		want := []string{"go", "golangci-lint", "markdownlint-cli2", "go-license", "govulncheck"}
		got := commandNames(runner.calls)
		if !slices.Equal(got, want) {
			t.Fatalf("call sequence = %+v, want %+v", got, want)
		}
	})

	t.Run("default mode runs every stage and aggregates the failures", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{decide: func(name string, args []string) error {
			if name == "go" && slices.Contains(args, "vet") {
				return errors.New("vet finding")
			}
			return nil
		}}

		in := Inputs{
			Root:     root,
			Modules:  []modules.Module{{Dir: "."}},
			GitFiles: stubGitFiles(),
		}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			errorprefix.Defaults(),
			stage.Filter{},
			stage.Options{},
		)
		if err == nil {
			t.Fatal("All returned nil, want aggregated vet error")
		}
		// Every subprocess-backed stage still records its call even
		// though vet failed — default (non-fast) mode runs all of
		// them and aggregates the failures.
		want := []string{"go", "golangci-lint", "markdownlint-cli2", "go-license", "govulncheck"}
		got := commandNames(runner.calls)
		if !slices.Equal(got, want) {
			t.Fatalf("call sequence = %+v, want %+v (default mode runs every stage)", got, want)
		}
	})

	t.Run("fast mode aborts at the first failing stage", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{decide: func(name string, args []string) error {
			if name == "go" && slices.Contains(args, "vet") {
				return errors.New("vet finding")
			}
			return nil
		}}

		in := Inputs{
			Root:     root,
			Modules:  []modules.Module{{Dir: "."}},
			GitFiles: stubGitFiles(),
		}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			errorprefix.Defaults(),
			stage.Filter{},
			stage.Options{Fast: true},
		)
		if err == nil {
			t.Fatal("All returned nil, want vet error")
		}
		postVet := map[string]bool{
			"golangci-lint": true, "markdownlint-cli2": true, "go-license": true, "govulncheck": true,
		}
		for _, c := range runner.calls {
			if postVet[c.name] {
				t.Fatalf("unexpected post-vet call: %q (fast mode should abort)", c.name)
			}
		}
	})

	t.Run("filter Only restricts the run to the named stages", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{
			Root:     root,
			Modules:  []modules.Module{{Dir: "."}},
			GitFiles: stubGitFiles(),
		}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			errorprefix.Defaults(),
			stage.Filter{Only: []string{"vet"}},
			stage.Options{},
		)
		if err != nil {
			t.Fatalf("All err: %v", err)
		}
		for _, c := range runner.calls {
			// `go vet` registers as name="go" with args containing "vet".
			isVet := c.name == "go" && slices.Contains(c.args, "vet")
			if !isVet {
				t.Fatalf("unexpected call %q with --only vet active", c.name)
			}
		}
	})

	t.Run("filter Disabled removes named stages from the run", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{
			Root:     root,
			Modules:  []modules.Module{{Dir: "."}},
			GitFiles: stubGitFiles(),
		}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			errorprefix.Defaults(),
			stage.Filter{Disabled: []string{"md", "license", "vuln"}},
			stage.Options{},
		)
		if err != nil {
			t.Fatalf("All err: %v", err)
		}
		for _, c := range runner.calls {
			if c.name == "markdownlint-cli2" || c.name == "go-license" || c.name == "govulncheck" {
				t.Fatalf("unexpected disabled-stage call: %q", c.name)
			}
		}
	})

	t.Run("filter with unknown stage surfaces ErrUnknownStage", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{
			Root:     root,
			Modules:  []modules.Module{{Dir: "."}},
			GitFiles: stubGitFiles(),
		}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			errorprefix.Defaults(),
			stage.Filter{Only: []string{"nonexistent"}},
			stage.Options{},
		)
		if err == nil {
			t.Fatal("All returned nil, want ErrUnknownStage")
		}
		if !errors.Is(err, stage.ErrUnknownStage) {
			t.Fatalf("err = %v, want wrapped ErrUnknownStage", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("recorded %d calls, want 0 (filter validation fails before any stage runs)", len(runner.calls))
		}
	})
}

// stubGitFiles returns a [Inputs.GitFiles] resolver that yields
// an empty file list. Lets the skip-expiry and error-prefix
// stages run cleanly in tests without wiring a real git
// invocation; those stages have their own coverage in
// `internal/checks/skipexpiry` and `internal/checks/errorprefix`.
func stubGitFiles() func() ([]string, error) {
	return func() ([]string, error) { return nil, nil }
}

// fakeRunner satisfies [xexec.Runner] for tests. It is safe for
// concurrent use: per-module fan-out under stage.PerModule's
// default (parallel) mode hands the same runner to every
// goroutine, so writes to calls go through mu.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []recordedCall
	runErr error
	decide func(name string, args []string) error
}

type recordedCall struct {
	dir  string
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	// filepath.ToSlash normalises Windows backslashes so the call
	// comparisons stay portable across operating systems.
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{dir: filepath.ToSlash(opts.Dir), name: name, args: slices.Clone(args)})
	f.mu.Unlock()
	if f.decide != nil {
		return f.decide(name, args)
	}
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// buildTree writes the named files to a fresh temp dir and returns
// the root.
func buildTree(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}

// commandNames extracts the name field of each recorded call.
func commandNames(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.name)
	}
	return out
}

// assertCalls compares the recorded calls (formatted as
// `<dir>: <name> <args>`) against want as a set: parallel
// per-module execution makes the recording order non-
// deterministic, so the comparison sorts both sides.
func assertCalls(t *testing.T, got []recordedCall, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", formatCalls(got), want)
	}
	gotSorted := slices.Clone(formatCalls(got))
	wantSorted := slices.Clone(want)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Fatalf("calls = %+v, want (set-equal) %+v", gotSorted, wantSorted)
	}
}

func formatCalls(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.dir+": "+c.name+" "+strings.Join(c.args, " "))
	}
	return out
}
