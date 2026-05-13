// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package license

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
)

// TestApply pins the contract of [Apply]: the discovered Go file
// list is passed to `go-license` with the configured config flag,
// no `--verify` flag is set, and the working directory is the
// repository root.
func TestApply(t *testing.T) {
	t.Parallel()

	t.Run("invokes go-license with discovered files and no --verify", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go", "pkg/sub.go", "vendor/skip.go", "x_test.go")
		runner := &fakeRunner{}

		err := Apply(t.Context(), runner, io.Discard, io.Discard, root, Defaults())
		if err != nil {
			t.Fatalf("Apply err: %v", err)
		}
		assertCall(t, runner.lastCall, root, "go-license",
			[]string{"--config=.go-license.yml", "main.go", "pkg/sub.go", "x_test.go"})
	})

	t.Run("no go files in tree means no subprocess invocation", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "README.md", "Makefile")
		runner := &fakeRunner{}

		if err := Apply(t.Context(), runner, io.Discard, io.Discard, root, Defaults()); err != nil {
			t.Fatalf("Apply err: %v", err)
		}
		if runner.lastCall.name != "" {
			t.Fatalf("expected no subprocess; got %+v", runner.lastCall)
		}
	})

	t.Run("config_file override propagates to the --config flag", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		err := Apply(t.Context(), runner, io.Discard, io.Discard, root,
			Config{ConfigFile: ".license.yml"})
		if err != nil {
			t.Fatalf("Apply err: %v", err)
		}
		if runner.lastCall.args[0] != "--config=.license.yml" {
			t.Fatalf("first arg = %q, want --config=.license.yml", runner.lastCall.args[0])
		}
	})

	t.Run("exclude_dirs override drops matching subtrees", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go", "internal/keep.go", "skipme/leaf.go")
		runner := &fakeRunner{}

		cfg := Config{ExcludeDirs: []string{"skipme"}}
		if err := Apply(t.Context(), runner, io.Discard, io.Discard, root, cfg); err != nil {
			t.Fatalf("Apply err: %v", err)
		}
		for _, a := range runner.lastCall.args {
			if strings.HasPrefix(a, "skipme/") {
				t.Fatalf("excluded dir leaked: %+v", runner.lastCall.args)
			}
		}
	})

	t.Run("default generated suffixes drop automatically", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go", "pkg.gen.go", "pkg.gen_test.go")
		runner := &fakeRunner{}

		if err := Apply(t.Context(), runner, io.Discard, io.Discard, root, Defaults()); err != nil {
			t.Fatalf("Apply err: %v", err)
		}
		for _, a := range runner.lastCall.args {
			if strings.HasSuffix(a, ".gen.go") || strings.HasSuffix(a, ".gen_test.go") {
				t.Fatalf("generated suffix leaked: %+v", runner.lastCall.args)
			}
		}
	})

	t.Run("exclude_files override drops matching basenames", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go", "x_mock.go")
		runner := &fakeRunner{}

		cfg := Config{ExcludeFiles: []string{"*_mock.go"}}
		if err := Apply(t.Context(), runner, io.Discard, io.Discard, root, cfg); err != nil {
			t.Fatalf("Apply err: %v", err)
		}
		if slices.Contains(runner.lastCall.args, "x_mock.go") {
			t.Fatalf("excluded file leaked: %+v", runner.lastCall.args)
		}
	})
}

// TestVerify confirms the verify mode emits the `--verify` flag in
// addition to the standard arguments.
func TestVerify(t *testing.T) {
	t.Parallel()

	t.Run("invokes go-license with --verify", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{}

		err := Verify(t.Context(), runner, io.Discard, io.Discard, root, Defaults())
		if err != nil {
			t.Fatalf("Verify err: %v", err)
		}
		if !slices.Contains(runner.lastCall.args, "--verify") {
			t.Fatalf("args = %+v, want --verify present", runner.lastCall.args)
		}
	})

	t.Run("non-zero exit from go-license surfaces as an error", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, "main.go")
		runner := &fakeRunner{runErr: errors.New("stale header")}

		err := Verify(t.Context(), runner, io.Discard, io.Discard, root, Defaults())
		if err == nil {
			t.Fatal("Verify returned nil, want error")
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. It records the
// last (and only) Run invocation.
type fakeRunner struct {
	lastCall recordedCall
	runErr   error
}

type recordedCall struct {
	dir  string
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.lastCall = recordedCall{dir: opts.Dir, name: name, args: slices.Clone(args)}
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// buildTree writes the named files (relative to a fresh temp dir),
// creating any necessary parent directories. Returns the temp root.
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

// assertCall fails the test when the recorded call does not match
// the expectations.
func assertCall(t *testing.T, got recordedCall, wantDir, wantName string, wantArgs []string) {
	t.Helper()
	if got.dir != wantDir {
		t.Fatalf("dir = %q, want %q", got.dir, wantDir)
	}
	if got.name != wantName {
		t.Fatalf("name = %q, want %q", got.name, wantName)
	}
	if !slices.Equal(got.args, wantArgs) {
		t.Fatalf("args = %+v, want %+v", got.args, wantArgs)
	}
}
