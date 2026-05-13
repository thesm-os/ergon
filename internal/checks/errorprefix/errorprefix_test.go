// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package errorprefix

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun pins the contract of [Run]: errors.New literals must
// start with the file's package name (optionally `<pkg>.<sub>:`);
// test files are exempt; TargetDirs narrows the scan.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("matching prefix passes", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"clock: instant is zero\")\n",
		})

		var stdout bytes.Buffer
		if err := Run(&stdout, io.Discard, root, files, Defaults()); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "0 violations") {
			t.Fatalf("stdout = %q, want clean summary", stdout.String())
		}
	})

	t.Run("missing prefix is a violation", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"instant is zero\")\n",
		})

		var stderr bytes.Buffer
		err := Run(io.Discard, &stderr, root, files, Defaults())
		if err == nil {
			t.Fatal("Run returned nil, want error")
		}
		if !strings.Contains(stderr.String(), "clock: ") {
			t.Fatalf("stderr = %q, want it to name the expected prefix", stderr.String())
		}
	})

	t.Run("sub-package qualifier (<pkg>.<sub>:) is accepted", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/kernel.go": "package kernel\n\nimport \"errors\"\n\n" +
				"var ErrPatch = errors.New(\"kernel.patch: malformed\")\n",
		})

		if err := Run(io.Discard, io.Discard, root, files, Defaults()); err != nil {
			t.Fatalf("Run err: %v, want sub-package qualifier to pass", err)
		}
	})

	t.Run("test files are not scanned", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/clock_test.go": "package clock\n\nimport \"errors\"\n\n" +
				"var TestErr = errors.New(\"no prefix\")\n",
		})

		if err := Run(io.Discard, io.Discard, root, files, Defaults()); err != nil {
			t.Fatalf("Run err: %v, want test files to be exempt", err)
		}
	})

	t.Run("TargetDirs narrows the scan", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"foundation/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"clock: zero\")\n",
			"cmd/main.go": "package main\n\nimport \"errors\"\n\n" +
				"var ErrCmd = errors.New(\"anything goes here\")\n",
		})

		cfg := Config{TargetDirs: []string{"foundation"}}
		if err := Run(io.Discard, io.Discard, root, files, cfg); err != nil {
			t.Fatalf("Run err: %v, want cmd/ to be ignored", err)
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
