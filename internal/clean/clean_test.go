// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package clean

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestRun pins the contract of [Run]: removes the three documented
// targets relative to root, silently tolerates missing ones, and
// names the project's per-name directory via [Targets].
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("removes bin, dist, and .<name> directories", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		for _, d := range []string{"bin", ".ergon", "dist"} {
			if err := os.MkdirAll(filepath.Join(root, d, "leaf"), 0o700); err != nil {
				t.Fatalf("mkdir %s: %v", d, err)
			}
		}

		if err := Run(io.Discard, root, "ergon"); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		for _, d := range []string{"bin", ".ergon", "dist"} {
			if _, err := os.Stat(filepath.Join(root, d)); !os.IsNotExist(err) {
				t.Errorf("%s still exists: err=%v", d, err)
			}
		}
	})

	t.Run("missing targets are tolerated (idempotent)", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir() // nothing exists
		if err := Run(io.Discard, root, "ergon"); err != nil {
			t.Fatalf("Run on empty tree err: %v", err)
		}
	})

	t.Run("Targets reflects the project name", func(t *testing.T) {
		t.Parallel()
		got := Targets("eidos")
		want := []string{"bin", ".eidos", "dist"}
		if len(got) != len(want) {
			t.Fatalf("Targets = %+v, want %+v", got, want)
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("Targets[%d] = %q, want %q", i, got[i], w)
			}
		}
	})
}
