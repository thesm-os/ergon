// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"go.thesmos.sh/ergon/internal/modules"
)

// TestModules pins the workspace-driven discovery rules: `go.work`
// `use` entries become the released modules, testdata entries
// drop, sort order puts root first then lexicographic, a missing
// workspace falls back to a single root entry, and an explicit
// override bypasses workspace reading entirely.
func TestModules(t *testing.T) {
	t.Parallel()

	t.Run("go.work use entries become modules in the documented order", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeWork(t, root, `go 1.26

use (
    ./
    ./cli
    ./frontend/golang
    ./frontend/protobuf
)
`)
		got, err := Modules(root, nil)
		if err != nil {
			t.Fatalf("Modules err: %v", err)
		}
		want := []string{".", "cli", "frontend/golang", "frontend/protobuf"}
		if !slices.Equal(moduleDirs(got), want) {
			t.Fatalf("dirs = %+v, want %+v", moduleDirs(got), want)
		}
	})

	t.Run("single-line use directives parse equivalently", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeWork(t, root, `go 1.26

use ./
use ./cli
use ./frontend/golang
`)
		got, err := Modules(root, nil)
		if err != nil {
			t.Fatalf("Modules err: %v", err)
		}
		want := []string{".", "cli", "frontend/golang"}
		if !slices.Equal(moduleDirs(got), want) {
			t.Fatalf("dirs = %+v, want %+v", moduleDirs(got), want)
		}
	})

	t.Run("testdata entries drop out of the result", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeWork(t, root, `go 1.26

use (
    ./
    ./cli
    ./cli/testdata/multipkg
    ./cli/testdata/demoproject
)
`)
		got, err := Modules(root, nil)
		if err != nil {
			t.Fatalf("Modules err: %v", err)
		}
		want := []string{".", "cli"}
		if !slices.Equal(moduleDirs(got), want) {
			t.Fatalf("dirs = %+v, want %+v", moduleDirs(got), want)
		}
	})

	t.Run("missing go.work falls back to a single root entry", func(t *testing.T) {
		t.Parallel()
		got, err := Modules(t.TempDir(), nil)
		if err != nil {
			t.Fatalf("Modules err: %v", err)
		}
		want := []string{"."}
		if !slices.Equal(moduleDirs(got), want) {
			t.Fatalf("dirs = %+v, want %+v", moduleDirs(got), want)
		}
	})

	t.Run("comments and quoted paths in go.work parse correctly", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeWork(t, root, `go 1.26

// release-tool consumes this file.
use (
    "./"           // root module
    ./cli          // command-line interface
)
`)
		got, err := Modules(root, nil)
		if err != nil {
			t.Fatalf("Modules err: %v", err)
		}
		want := []string{".", "cli"}
		if !slices.Equal(moduleDirs(got), want) {
			t.Fatalf("dirs = %+v, want %+v", moduleDirs(got), want)
		}
	})

	t.Run("workspace with only testdata entries surfaces an error", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeWork(t, root, `go 1.26

use (
    ./testdata/a
    ./testdata/b
)
`)
		_, err := Modules(root, nil)
		if err == nil {
			t.Fatalf("Modules returned nil error for testdata-only workspace")
		}
	})

	t.Run("empty go.work surfaces an error", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeWork(t, root, "go 1.26\n")
		_, err := Modules(root, nil)
		if err == nil {
			t.Fatalf("Modules with empty workspace returned nil error")
		}
	})

	t.Run("override bypasses workspace reading", func(t *testing.T) {
		t.Parallel()
		// A go.work exists with one shape; the override declares
		// a different set. The override wins.
		root := t.TempDir()
		writeWork(t, root, `go 1.26

use ./shouldnt-show-up
`)
		got, err := Modules(root, []string{".", "./cli", "frontend/golang"})
		if err != nil {
			t.Fatalf("Modules err: %v", err)
		}
		want := []string{".", "cli", "frontend/golang"}
		if !slices.Equal(moduleDirs(got), want) {
			t.Fatalf("dirs = %+v, want %+v", moduleDirs(got), want)
		}
	})
}

// TestRoot pins the `git rev-parse --show-toplevel` lookup —
// in a real temp git repository the function should return the
// repository root absolute path; outside a git tree it should
// surface a wrapped error.
func TestRoot(t *testing.T) {
	// Cannot use t.Parallel() — subtests call t.Chdir, which the
	// test framework refuses to combine with parallelism.

	t.Run("inside a git repo returns the repository root", func(t *testing.T) {
		dir := initGitRepo(t)
		t.Chdir(dir)
		got, err := Root(t.Context())
		if err != nil {
			t.Fatalf("Root err: %v", err)
		}
		// macOS prepends /private to TempDir paths; resolve both
		// sides to absolute symlink-evaluated form.
		wantAbs, _ := filepath.EvalSymlinks(dir)
		gotAbs, _ := filepath.EvalSymlinks(got)
		if gotAbs != wantAbs {
			t.Fatalf("Root = %q, want %q", gotAbs, wantAbs)
		}
	})

	t.Run("outside a git repo surfaces the wrapped git error", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		_, err := Root(t.Context())
		if err == nil {
			t.Fatal("Root err = nil outside git repo, want non-nil")
		}
	})
}

// TestResolve pins the [Root] + [Modules] composition: the
// override flow does not require workspace files and produces the
// canonical module set verbatim.
func TestResolve(t *testing.T) {
	// Cannot use t.Parallel() — subtests call t.Chdir, which the
	// test framework refuses to combine with parallelism.

	t.Run("override-driven flow does not need a workspace", func(t *testing.T) {
		dir := initGitRepo(t)
		t.Chdir(dir)
		root, mods, err := Resolve(t.Context(), []string{".", "./cli"})
		if err != nil {
			t.Fatalf("Resolve err: %v", err)
		}
		if root == "" {
			t.Fatal("Resolve root = empty")
		}
		if !slices.Equal(moduleDirs(mods), []string{".", "cli"}) {
			t.Fatalf("mods = %+v, want [., cli]", moduleDirs(mods))
		}
	})

	t.Run("outside a git repo surfaces the wrapped git error", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		_, _, err := Resolve(t.Context(), nil)
		if err == nil {
			t.Fatal("Resolve err = nil outside git repo, want non-nil")
		}
	})
}

// initGitRepo creates a tempdir, runs `git init` inside it, and
// returns the absolute path. Used by the [Root] + [Resolve] tests
// that need a real repository to walk.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

// writeWork writes body to <dir>/go.work and fails the test on error.
func writeWork(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.work"), []byte(body), 0o600); err != nil {
		t.Fatalf("write go.work: %v", err)
	}
}

// moduleDirs flattens a []Module into its Dir fields in receive order.
func moduleDirs(mods []modules.Module) []string {
	out := make([]string, 0, len(mods))
	for _, m := range mods {
		out = append(out, m.Dir)
	}
	return out
}
