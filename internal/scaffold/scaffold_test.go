// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package scaffold

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun pins the contract of [Run]: writes every templated file
// into dest, substitutes the Name variable, refuses to overwrite
// without force, and tolerates --force on an existing tree.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("writes the canonical file set into a fresh dest", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		if err := Run(io.Discard, dest, Vars{Name: "example"}, false); err != nil {
			t.Fatalf("Run err: %v", err)
		}

		want := []string{
			"Makefile",
			".ergon.yaml",
			".gitignore",
			".editorconfig",
			".pre-commit-config.yaml",
			".golangci.yml",
			"README.md",
			".github/workflows/ci.yml",
		}
		for _, rel := range want {
			if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
				t.Errorf("missing %s: %v", rel, err)
			}
		}
	})

	t.Run("Name substitutes into the templates", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		if err := Run(io.Discard, dest, Vars{Name: "myproj"}, false); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(dest, ".ergon.yaml"))
		if err != nil {
			t.Fatalf("read .ergon.yaml: %v", err)
		}
		if !strings.Contains(string(body), "name: myproj") {
			t.Fatalf(".ergon.yaml = %q, want `name: myproj`", string(body))
		}
	})

	t.Run("existing destination file is skipped without force; siblings still write", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		if err := os.WriteFile(filepath.Join(dest, "Makefile"), []byte("custom"), 0o600); err != nil {
			t.Fatalf("seed Makefile: %v", err)
		}
		var stdout strings.Builder
		if err := Run(&stdout, dest, Vars{Name: "myproj"}, false); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(dest, "Makefile"))
		if err != nil {
			t.Fatalf("read Makefile: %v", err)
		}
		if string(body) != "custom" {
			t.Fatalf("Makefile overwritten without force: %q", string(body))
		}
		if _, err := os.Stat(filepath.Join(dest, ".ergon.yaml")); err != nil {
			t.Fatalf("siblings did not write: %v", err)
		}
		if !strings.Contains(stdout.String(), "skipped Makefile") {
			t.Fatalf("stdout = %q, want skip notice for Makefile", stdout.String())
		}
	})

	t.Run("force overwrites an existing file", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		if err := os.WriteFile(filepath.Join(dest, "Makefile"), []byte("custom"), 0o600); err != nil {
			t.Fatalf("seed Makefile: %v", err)
		}
		if err := Run(io.Discard, dest, Vars{Name: "myproj"}, true); err != nil {
			t.Fatalf("Run force err: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(dest, "Makefile"))
		if err != nil {
			t.Fatalf("read Makefile: %v", err)
		}
		if !strings.Contains(string(body), "$(ERGON) bootstrap") {
			t.Fatalf("Makefile not overwritten: %s", string(body))
		}
	})

	t.Run("--ergon.yaml renames from ergon.yaml.tmpl", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		if err := Run(io.Discard, dest, Vars{Name: "x"}, false); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dest, ".ergon.yaml")); err != nil {
			t.Fatalf(".ergon.yaml missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dest, "ergon.yaml")); err == nil {
			t.Fatalf("ergon.yaml exists; the rename did not apply")
		}
	})
}
