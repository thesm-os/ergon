// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package generate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/format"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// TestRun pins the orchestration: `go generate ./...` runs per
// module, then [format.Run] runs after. A generate failure
// short-circuits before the format pipeline starts.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("invokes `go generate ./...` per module then format", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}
		in := format.Inputs{
			Root:       root,
			ImportPath: "go.example.com/proj",
			Modules:    []modules.Module{{Dir: "."}, {Dir: "cli"}},
		}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in,
			license.Defaults(), markdown.Defaults(), stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		names := commandNames(runner.calls)
		generateCount := 0
		for _, n := range names {
			if n == "go generate" {
				generateCount++
			}
		}
		if generateCount != 2 {
			t.Fatalf("go generate calls = %d, want 2 (one per module)", generateCount)
		}
		// Format adds at least go-license, gofumpt, gci, markdownlint.
		if !slices.Contains(names, "go-license") {
			t.Fatalf("missing go-license call; got %+v", names)
		}
	})

	t.Run("generate failure aborts before the format pipeline runs", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{decide: func(name string, args []string) error {
			if name == "go" && len(args) > 0 && args[0] == "generate" {
				return errors.New("generate failed")
			}
			return nil
		}}
		in := format.Inputs{
			Root: root, ImportPath: "p",
			Modules: []modules.Module{{Dir: "."}},
		}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in,
			license.Defaults(), markdown.Defaults(), stage.Options{})
		if err == nil {
			t.Fatal("Run err = nil, want generate failure")
		}
		for _, c := range runner.calls {
			if c.name == "go-license" {
				t.Fatalf("format stage ran after generate failure; calls = %+v", runner.calls)
			}
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for the generate tests. The
// mutex makes it safe under stage.PerModule's concurrent fan-out.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []recordedCall
	decide func(name string, args []string) error
}

type recordedCall struct {
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, _ xexec.Options, name string, args ...string) error {
	f.mu.Lock()
	// `go generate` and `go build` both surface as `go` with the
	// subcommand in args[0]; collapse them to a stable name for
	// the assertions.
	display := name
	if name == "go" && len(args) > 0 {
		display = "go " + args[0]
	}
	f.calls = append(f.calls, recordedCall{name: display, args: slices.Clone(args)})
	f.mu.Unlock()
	if f.decide != nil {
		return f.decide(name, args)
	}
	return nil
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// buildTree mirrors the format-package helper: writes the named
// files to a fresh temp dir with a placeholder `package x` body
// and returns the root.
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

// commandNames extracts the recorded call display names so order
// assertions can ignore per-arg noise.
func commandNames(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.name)
	}
	return out
}
