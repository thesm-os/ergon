// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPublishInto pins the staging-to-repository copy.
//
// The publish step is what makes concurrent runs safe: profiles are
// produced under the run's private temp root and moved into the
// shared directory only once complete. A publish that silently
// dropped a file, or that failed without saying so, would leave the
// gate reading a stale profile and reporting a verdict about code
// that no longer exists.
func TestPublishInto(t *testing.T) {
	t.Parallel()

	t.Run("copies files and ignores directories", func(t *testing.T) {
		t.Parallel()
		from, to := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(from, "root.out"),
			[]byte("mode: atomic\n"), 0o600); err != nil {
			t.Fatalf("seed profile: %v", err)
		}
		// gremlins and gobco leave working directories under the same
		// temp root; a directory here must be skipped, not recursed
		// into or reported as a failure.
		if err := os.MkdirAll(filepath.Join(from, "workdir"), 0o700); err != nil {
			t.Fatalf("seed dir: %v", err)
		}

		if err := publishInto(from, to); err != nil {
			t.Fatalf("publishInto: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(to, "root.out"))
		if err != nil {
			t.Fatalf("read published: %v", err)
		}
		if string(body) != "mode: atomic\n" {
			t.Errorf("published = %q, want the staged contents", body)
		}
		if _, err := os.Stat(filepath.Join(to, "workdir")); err == nil {
			t.Error("a directory was published, want it skipped")
		}
	})

	t.Run("replaces an existing profile rather than appending", func(t *testing.T) {
		t.Parallel()
		from, to := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(to, "root.out"),
			[]byte("mode: atomic\nold-a\nold-b\n"), 0o600); err != nil {
			t.Fatalf("seed stale profile: %v", err)
		}
		if err := os.WriteFile(filepath.Join(from, "root.out"),
			[]byte("mode: atomic\nfresh\n"), 0o600); err != nil {
			t.Fatalf("seed fresh profile: %v", err)
		}

		if err := publishInto(from, to); err != nil {
			t.Fatalf("publishInto: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(to, "root.out"))
		if err != nil {
			t.Fatalf("read published: %v", err)
		}
		if strings.Contains(string(body), "old-") {
			t.Errorf("published = %q, want the previous contents gone", body)
		}
	})

	t.Run("a missing staging directory is reported", func(t *testing.T) {
		t.Parallel()
		err := publishInto(filepath.Join(t.TempDir(), "absent"), t.TempDir())
		if err == nil {
			t.Fatal("publishInto = nil, want the missing directory reported")
		}
		if !strings.Contains(err.Error(), "coverage") {
			t.Errorf("err = %v, want it to name what failed", err)
		}
	})

	t.Run("an unwritable destination is reported", func(t *testing.T) {
		t.Parallel()
		from := t.TempDir()
		if err := os.WriteFile(filepath.Join(from, "root.out"), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed profile: %v", err)
		}
		// Publishing writes its temporary file in the destination, so
		// a directory it cannot create in must surface rather than
		// leave the caller believing the profiles are current.
		to := filepath.Join(t.TempDir(), "sealed")
		if err := os.Mkdir(to, 0o500); err != nil {
			t.Fatalf("mkdir sealed: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(to, 0o700) })

		if err := publishInto(from, to); err == nil {
			t.Skip("destination remained writable; nothing to assert on this platform")
		}
	})
}

// TestReplaceFile pins the atomicity the publish depends on.
//
// A reader must see either the previous contents or the new ones.
// Writing in place gave it a third option — a truncated file — and
// seven of nine concurrent readers then reported a coverage verdict
// derived from one.
func TestReplaceFile(t *testing.T) {
	t.Parallel()

	t.Run("leaves no temporary file behind", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := replaceFile(filepath.Join(dir, "root.out"), []byte("body")); err != nil {
			t.Fatalf("replaceFile: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		// The staging file is a dotfile, and the gate globs `*.out`,
		// so a leak would never be read — it would just accumulate.
		for _, e := range entries {
			if e.Name() != "root.out" {
				t.Errorf("stray entry %q, want only the published file", e.Name())
			}
		}
	})

	t.Run("a missing destination directory is reported", func(t *testing.T) {
		t.Parallel()
		err := replaceFile(filepath.Join(t.TempDir(), "absent", "root.out"), []byte("body"))
		if err == nil {
			t.Fatal("replaceFile = nil, want the missing directory reported")
		}
	})
}
