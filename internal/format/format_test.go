// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package format

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/modules"
)

// TestRun pins the orchestration order and the per-step behaviour
// of `ergon fmt`. The contract under test: license headers go on
// first, then gofumpt + gci run per module in declared order,
// then markdownlint runs once at the workspace level; gofumpt and
// gci failures abort the run while markdown lint failures degrade
// to a stderr warning.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("orchestrates license, gofumpt, gci, markdownlint in order", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{
			Root:       root,
			ImportPath: "go.example.com/proj",
			Modules:    []modules.Module{{Dir: "."}, {Dir: "cli"}},
		}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), license.Defaults())
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		names := commandNames(runner.calls)
		want := []string{"go-license", "gofumpt", "gci", "gofumpt", "gci", "markdownlint-cli2"}
		if !slices.Equal(names, want) {
			t.Fatalf("call sequence = %+v, want %+v", names, want)
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
		if err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), license.Defaults()); err != nil {
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
		runner := &fakeRunner{
			decide: func(name string, _ []string) error {
				if name == "go-license" {
					return errors.New("stale header")
				}
				return nil
			},
		}

		in := Inputs{Root: root, ImportPath: "p", Modules: []modules.Module{{Dir: "."}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), license.Defaults())
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
		runner := &fakeRunner{
			decide: func(name string, _ []string) error {
				if name == "gofumpt" {
					return errors.New("boom")
				}
				return nil
			},
		}

		in := Inputs{Root: root, ImportPath: "p", Modules: []modules.Module{{Dir: "."}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, in, Defaults(), license.Defaults())
		if err == nil {
			t.Fatal("Run returned nil, want gofumpt error")
		}
		if slices.ContainsFunc(runner.calls, func(c recordedCall) bool { return c.name == "gci" }) {
			t.Fatalf("gci ran after gofumpt failure; calls = %+v", runner.calls)
		}
	})

	t.Run("markdownlint failure degrades to a stderr warning", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{
			decide: func(name string, _ []string) error {
				if name == "markdownlint-cli2" {
					return errors.New("md errors")
				}
				return nil
			},
		}

		var stderr bytes.Buffer
		in := Inputs{Root: root, ImportPath: "p", Modules: []modules.Module{{Dir: "."}}}
		err := Run(t.Context(), runner, io.Discard, &stderr, in, Defaults(), license.Defaults())
		if err != nil {
			t.Fatalf("Run err: %v, want nil (md failure is warning)", err)
		}
		if !strings.Contains(stderr.String(), "markdownlint") {
			t.Fatalf("stderr = %q, want warning about markdownlint", stderr.String())
		}
	})

	t.Run("empty markdown globs skip the markdownlint step", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		in := Inputs{Root: root, ImportPath: "p", Modules: []modules.Module{{Dir: "."}}}
		cfg := Config{MarkdownGlobs: nil}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, in, cfg, license.Defaults()); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if slices.ContainsFunc(runner.calls, func(c recordedCall) bool { return c.name == "markdownlint-cli2" }) {
			t.Fatalf("markdownlint ran with empty globs; calls = %+v", runner.calls)
		}
	})

	t.Run("missing markdownlint-cli2 surfaces a clean warning and skips", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{lookPath: func(name string) (string, error) {
			if name == "markdownlint-cli2" {
				return "", exec.ErrNotFound
			}
			return "/usr/local/bin/" + name, nil
		}}

		var stderr bytes.Buffer
		in := Inputs{Root: root, ImportPath: "p", Modules: []modules.Module{{Dir: "."}}}
		if err := Run(t.Context(), runner, io.Discard, &stderr, in, Defaults(), license.Defaults()); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if slices.ContainsFunc(runner.calls, func(c recordedCall) bool { return c.name == "markdownlint-cli2" }) {
			t.Fatalf("markdownlint-cli2 ran despite missing binary; calls = %+v", runner.calls)
		}
		if !strings.Contains(stderr.String(), "not on PATH") {
			t.Fatalf("stderr = %q, want warning mentioning `not on PATH`", stderr.String())
		}
		if !strings.Contains(stderr.String(), "ergon bootstrap") {
			t.Fatalf("stderr = %q, want hint to run `ergon bootstrap`", stderr.String())
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. Each call is
// recorded; optional decide returns a per-call error; optional
// lookPath overrides the default "everything is present" policy.
type fakeRunner struct {
	calls    []recordedCall
	decide   func(name string, args []string) error
	lookPath func(name string) (string, error)
}

type recordedCall struct {
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, _ xexec.Options, name string, args ...string) error {
	f.calls = append(f.calls, recordedCall{name: name, args: slices.Clone(args)})
	if f.decide != nil {
		return f.decide(name, args)
	}
	return nil
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.lookPath != nil {
		return f.lookPath(name)
	}
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
