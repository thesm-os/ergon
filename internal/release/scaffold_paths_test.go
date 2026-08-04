// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestReadScaffoldModulePathRejects covers the three ways a go.mod
// fails to yield a module path. Each error names the offending file
// because the scaffold walks upward and may land on a go.mod the
// user did not expect it to read.
func TestReadScaffoldModulePathRejects(t *testing.T) {
	t.Parallel()

	t.Run("unreadable file", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "go.mod")
		_, err := readScaffoldModulePath(missing)
		if err == nil {
			t.Fatal("readScaffoldModulePath returned nil, want the read failure")
		}
		if !strings.Contains(err.Error(), "go.mod") {
			t.Errorf("err = %v, want it to name the file", err)
		}
	})

	t.Run("unparseable contents", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(t.TempDir(), "go.mod")
		if err := os.WriteFile(p, []byte("module\x00 not a go.mod {{{\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := readScaffoldModulePath(p); err == nil {
			t.Fatal("readScaffoldModulePath returned nil, want the parse failure")
		}
	})

	t.Run("no module directive", func(t *testing.T) {
		t.Parallel()
		// Valid go.mod syntax, but nothing to stamp ldflags against.
		p := filepath.Join(t.TempDir(), "go.mod")
		if err := os.WriteFile(p, []byte("go 1.26\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := readScaffoldModulePath(p)
		if err == nil {
			t.Fatal("readScaffoldModulePath returned nil, want the missing-directive error")
		}
		if !strings.Contains(err.Error(), "no module directive") {
			t.Errorf("err = %v, want the missing-directive diagnostic", err)
		}
	})

	t.Run("a well-formed go.mod yields its module path", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(t.TempDir(), "go.mod")
		if err := os.WriteFile(p, []byte("module example.test/proj\n\ngo 1.26\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := readScaffoldModulePath(p)
		if err != nil {
			t.Fatalf("readScaffoldModulePath: %v", err)
		}
		if got != "example.test/proj" {
			t.Errorf("module = %q, want example.test/proj", got)
		}
	})
}

// TestGoReleaserPath pins the path shape goreleaser's `dir:` and
// `main:` fields expect: "." stays bare, an already-relative path
// is preserved, and a bare path gains the `./` prefix.
func TestGoReleaserPath(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{".", "."},
		{"cmd/foo", "./cmd/foo"},
		{"./cmd/foo", "./cmd/foo"},
		{"../sibling", "../sibling"},
		{"cli", "./cli"},
	}
	for _, tc := range cases {
		if got := goReleaserPath(tc.in); got != tc.want {
			t.Errorf("goReleaserPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestResolveBuildSpecNoModule covers the sentinel path: the
// upward walk stops at dest, so a tree with no go.mod anywhere
// surfaces ErrNoEnclosingModule rather than escaping the project
// root and picking up an unrelated module.
func TestResolveBuildSpecNoModule(t *testing.T) {
	t.Parallel()

	_, err := ResolveBuildSpec(t.TempDir(), "./cmd/proj")
	if err == nil {
		t.Fatal("ResolveBuildSpec returned nil, want ErrNoEnclosingModule")
	}
	if !errors.Is(err, ErrNoEnclosingModule) {
		t.Errorf("err = %v, want it to wrap ErrNoEnclosingModule", err)
	}
}

// TestScaffoldWriteFailure covers the write-error path: a
// destination the process cannot write to aborts with the failure
// surfaced rather than silently producing a partial pipeline.
func TestScaffoldWriteFailure(t *testing.T) {
	t.Parallel()
	// The unwritable destination is produced with chmod, which only
	// denies writes where POSIX mode bits are enforced. On Windows
	// os.Chmod toggles the read-only attribute and does not stop
	// file creation inside a directory, so the write would succeed
	// and the assertion below would be testing nothing.
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not deny directory writes on Windows")
	}
	// Root bypasses mode bits entirely. Geteuid reports -1 on
	// Windows, so this check has to follow the GOOS guard.
	if os.Geteuid() == 0 {
		t.Skip("running as root; mode bits do not deny writes")
	}

	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "go.mod"),
		[]byte("module example.test/proj\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	spec, err := ResolveBuildSpec(dest, "./cmd/proj")
	if err != nil {
		t.Fatalf("ResolveBuildSpec: %v", err)
	}

	// Deny writes to the destination so the first template write
	// fails. Restored by t.Cleanup so TempDir removal succeeds.
	if chmodErr := os.Chmod(dest, 0o500); chmodErr != nil {
		t.Fatalf("chmod: %v", chmodErr)
	}
	t.Cleanup(func() { _ = os.Chmod(dest, 0o700) })

	err = Scaffold(io.Discard, dest, ScaffoldVars{
		Name: "proj", Builds: []BuildSpec{spec},
	}, false)
	if err == nil {
		t.Fatal("Scaffold returned nil, want the write failure surfaced")
	}
}
