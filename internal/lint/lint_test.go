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
	"testing"

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

// TestAll pins the orchestration order. The contract: vet -> go ->
// markdown -> license; any failure short-circuits the remaining
// stages. Inputs need a real filesystem for license to walk.
func TestAll(t *testing.T) {
	t.Parallel()

	t.Run("orchestrates vet, golangci-lint, markdown, license in order", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			stage.Options{},
		)
		if err != nil {
			t.Fatalf("All err: %v", err)
		}
		want := []string{"go", "golangci-lint", "markdownlint-cli2", "go-license"}
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

		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			stage.Options{},
		)
		if err == nil {
			t.Fatal("All returned nil, want aggregated vet error")
		}
		// Every stage must have run.
		want := []string{"go", "golangci-lint", "markdownlint-cli2", "go-license"}
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

		in := Inputs{Root: root, Modules: []modules.Module{{Dir: "."}}}
		err := All(
			t.Context(),
			runner,
			io.Discard,
			io.Discard,
			in,
			markdown.Defaults(),
			license.Defaults(),
			stage.Options{Fast: true},
		)
		if err == nil {
			t.Fatal("All returned nil, want vet error")
		}
		postVet := map[string]bool{
			"golangci-lint": true, "markdownlint-cli2": true, "go-license": true,
		}
		for _, c := range runner.calls {
			if postVet[c.name] {
				t.Fatalf("unexpected post-vet call: %q (fast mode should abort)", c.name)
			}
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests.
type fakeRunner struct {
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
	f.calls = append(f.calls, recordedCall{dir: opts.Dir, name: name, args: slices.Clone(args)})
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
// `<dir>: <name> <args>`) against want.
func assertCalls(t *testing.T, got []recordedCall, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", formatCalls(got), want)
	}
	formatted := formatCalls(got)
	for i, w := range want {
		if formatted[i] != w {
			t.Fatalf("calls[%d] = %q, want %q", i, formatted[i], w)
		}
	}
}

func formatCalls(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.dir+": "+c.name+" "+strings.Join(c.args, " "))
	}
	return out
}
