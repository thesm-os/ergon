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
// test files are exempt; missing TargetDirs subtrees are skipped
// silently.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("matching prefix passes", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, map[string]string{
			"pkg/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"clock: instant is zero\")\n",
		})

		var stdout bytes.Buffer
		if err := Run(&stdout, io.Discard, root, Defaults()); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "0 violations") {
			t.Fatalf("stdout = %q, want clean summary", stdout.String())
		}
	})

	t.Run("missing prefix is a violation", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, map[string]string{
			"pkg/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"instant is zero\")\n",
		})

		var stderr bytes.Buffer
		err := Run(io.Discard, &stderr, root, Defaults())
		if err == nil {
			t.Fatal("Run returned nil, want error")
		}
		if !strings.Contains(stderr.String(), "clock: ") {
			t.Fatalf("stderr = %q, want it to name the expected prefix", stderr.String())
		}
	})

	t.Run("sub-package qualifier (<pkg>.<sub>:) is accepted", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, map[string]string{
			"pkg/kernel.go": "package kernel\n\nimport \"errors\"\n\n" +
				"var ErrPatch = errors.New(\"kernel.patch: malformed\")\n",
		})

		if err := Run(io.Discard, io.Discard, root, Defaults()); err != nil {
			t.Fatalf("Run err: %v, want sub-package qualifier to pass", err)
		}
	})

	t.Run("wrong prefix surfaces with expected prefix in the message", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, map[string]string{
			"pkg/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrWrong = errors.New(\"other: wrong package\")\n",
		})

		var stderr bytes.Buffer
		err := Run(io.Discard, &stderr, root, Defaults())
		if err == nil {
			t.Fatal("Run returned nil, want error")
		}
		if !strings.Contains(stderr.String(), `errors.New("other: wrong package")`) {
			t.Fatalf("stderr = %q, want literal echoed", stderr.String())
		}
	})

	t.Run("test files are not scanned", func(t *testing.T) {
		t.Parallel()
		root := buildTree(t, map[string]string{
			"pkg/clock_test.go": "package clock\n\nimport \"errors\"\n\n" +
				"var TestErr = errors.New(\"no prefix\")\n",
		})

		if err := Run(io.Discard, io.Discard, root, Defaults()); err != nil {
			t.Fatalf("Run err: %v, want test files to be exempt", err)
		}
	})

	t.Run("missing target dir is a silent skip", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		cfg := Config{TargetDirs: []string{"does-not-exist"}}

		if err := Run(io.Discard, io.Discard, root, cfg); err != nil {
			t.Fatalf("Run err: %v, want missing dir to be tolerated", err)
		}
	})
}

// buildTree writes files into a fresh tempdir and returns the root.
func buildTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}
