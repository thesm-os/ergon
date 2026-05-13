// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package skipexpiry

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun pins the contract of [Run]: scans the supplied list of
// repo-relative paths for `_test.go` files, reports expired
// skips, and treats future-dated skips as fine.
func TestRun(t *testing.T) {
	originalToday := Today
	defer func() { Today = originalToday }()
	Today = func() string { return "2026-06-01" }

	t.Run("expired skip surfaces with file path and line number", func(t *testing.T) {
		root, files := buildFiles(t, map[string]string{
			"pkg/a_test.go": "package pkg\n\nfunc TestA(t *testing.T) {\n" +
				"\tt.Skip(\"TODO(owner): reason — expires 2025-01-01\")\n}\n",
		})

		var stderr bytes.Buffer
		err := Run(io.Discard, &stderr, root, files)
		if err == nil {
			t.Fatal("Run returned nil, want error")
		}
		if !strings.Contains(stderr.String(), "EXPIRED") {
			t.Fatalf("stderr = %q, want EXPIRED line", stderr.String())
		}
	})

	t.Run("future-dated skip passes without error", func(t *testing.T) {
		root, files := buildFiles(t, map[string]string{
			"pkg/a_test.go": "package pkg\n\nfunc TestA(t *testing.T) {\n" +
				"\tt.Skip(\"TODO(owner): reason — expires 2099-12-31\")\n}\n",
		})

		var stdout bytes.Buffer
		if err := Run(&stdout, io.Discard, root, files); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "1 skip(s) scanned, 0 expired") {
			t.Fatalf("stdout = %q, want summary line", stdout.String())
		}
	})

	t.Run("today is on the expired side of the comparison", func(t *testing.T) {
		root, files := buildFiles(t, map[string]string{
			"pkg/a_test.go": "package pkg\n\nfunc TestA(t *testing.T) {\n" +
				"\tt.Skip(\"TODO: reason — expires 2026-06-01\")\n}\n",
		})

		if err := Run(io.Discard, io.Discard, root, files); err != nil {
			t.Fatalf("Run err: %v, want today date to be tolerated", err)
		}
	})

	t.Run("non-test files are ignored", func(t *testing.T) {
		root, files := buildFiles(t, map[string]string{
			"main.go": "package x\n\nfunc main() { Skip(\"expires 2020-01-01\") }\n",
		})

		var stdout bytes.Buffer
		if err := Run(&stdout, io.Discard, root, files); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "no t.Skip") {
			t.Fatalf("stdout = %q, want no-skips message", stdout.String())
		}
	})

	t.Run("undated skip is not reported", func(t *testing.T) {
		root, files := buildFiles(t, map[string]string{
			"pkg/a_test.go": "package pkg\n\nfunc TestA(t *testing.T) {\n" +
				"\tt.Skip(\"flaky for now\")\n}\n",
		})

		if err := Run(io.Discard, io.Discard, root, files); err != nil {
			t.Fatalf("Run err: %v, want undated skip to pass", err)
		}
	})
}

// buildFiles writes files into a fresh tempdir and returns the
// root plus the list of repo-relative paths.
func buildFiles(t *testing.T, contents map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var paths []string
	for rel, body := range contents {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
		paths = append(paths, rel)
	}
	return root, paths
}
