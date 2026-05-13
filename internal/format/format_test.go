// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package format

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
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// TestRun pins the orchestration order and the per-step behaviour
// of `ergon fmt`. The contract under test: license headers go on
// first, then gofumpt + gci run per module, then markdownlint
// runs once at the workspace level. Per-module fan-out is
// concurrent, so order assertions are framed around section
// boundaries rather than recorded-call indices.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("license first, gofumpt+gci per module, markdownlint last", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{
			Root:       root,
			ImportPath: "go.example.com/proj",
			Modules:    []modules.Module{{Dir: "."}, {Dir: "cli"}},
		}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in,
			license.Defaults(), markdown.Defaults(), stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}

		// license must be the first call and markdownlint the last
		// — the gofumpt/gci block in between is concurrent so its
		// internal order is not asserted.
		names := commandNames(runner.calls)
		if names[0] != "go-license" {
			t.Fatalf("first call = %q, want go-license", names[0])
		}
		if names[len(names)-1] != "markdownlint-cli2" {
			t.Fatalf("last call = %q, want markdownlint-cli2", names[len(names)-1])
		}
		// Two modules × (gofumpt + gci) = 4 fmt calls, plus
		// go-license + markdownlint = 6 total.
		if len(names) != 6 {
			t.Fatalf("calls = %d (%+v), want 6", len(names), names)
		}
		// Each module produced one gofumpt and one gci call.
		counts := map[string]int{}
		for _, n := range names {
			counts[n]++
		}
		if counts["gofumpt"] != 2 || counts["gci"] != 2 {
			t.Fatalf("counts = %+v, want gofumpt=2 gci=2", counts)
		}
	})

	t.Run("gci receives the configured import-path prefix", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{
			Root:       root,
			ImportPath: "go.example.com/proj",
			Modules:    []modules.Module{{Dir: "."}},
		}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in,
			license.Defaults(), markdown.Defaults(), stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		gciCall := findCall(t, runner.calls, "gci")
		if !slices.Contains(gciCall.args, "prefix(go.example.com/proj)") {
			t.Fatalf("gci args = %+v, want prefix(go.example.com/proj)", gciCall.args)
		}
	})

	t.Run("license failure aborts before gofumpt runs", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{decide: func(name string, _ []string) error {
			if name == "go-license" {
				return errors.New("stale header")
			}
			return nil
		}}

		in := Inputs{Root: root, ImportPath: "p", Modules: []modules.Module{{Dir: "."}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in,
			license.Defaults(), markdown.Defaults(), stage.Options{})
		if err == nil {
			t.Fatal("Run returned nil, want license error")
		}
		if slices.ContainsFunc(runner.calls, func(c recordedCall) bool { return c.name == "gofumpt" }) {
			t.Fatalf("gofumpt ran after license failure; calls = %+v", runner.calls)
		}
	})

	t.Run("gofumpt failure aborts before gci runs in that module", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{decide: func(name string, _ []string) error {
			if name == "gofumpt" {
				return errors.New("boom")
			}
			return nil
		}}

		in := Inputs{Root: root, ImportPath: "p", Modules: []modules.Module{{Dir: "."}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in,
			license.Defaults(), markdown.Defaults(), stage.Options{})
		if err == nil {
			t.Fatal("Run returned nil, want gofumpt error")
		}
		if slices.ContainsFunc(runner.calls, func(c recordedCall) bool { return c.name == "gci" }) {
			t.Fatalf("gci ran after gofumpt failure; calls = %+v", runner.calls)
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. The mutex makes
// it safe under stage.PerModule's default (parallel) fan-out.
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
	f.calls = append(f.calls, recordedCall{name: name, args: slices.Clone(args)})
	f.mu.Unlock()
	if f.decide != nil {
		return f.decide(name, args)
	}
	return nil
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

// commandNames extracts the command names from a recorded call
// sequence so tests can assert on order without per-arg noise.
func commandNames(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.name)
	}
	return out
}

// findCall returns the first recorded call with the given name and
// fails the test when no such call exists.
func findCall(t *testing.T, calls []recordedCall, name string) recordedCall {
	t.Helper()
	for _, c := range calls {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no %q call recorded; got %+v", name, calls)
	return recordedCall{}
}
