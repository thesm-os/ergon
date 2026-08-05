// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package runtmp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNew pins the isolation contract.
//
// Two ergon runs used to share /tmp directly: coverage merges,
// staged modfiles, gobco's module copies and gremlins' workdirs all
// landed in one directory, and a run could observe or clobber
// another's scratch space. Each invocation now owns a subtree.
//
// Not parallel: New calls os.Setenv, which mutates process-wide
// state and races any concurrent os.Environ read.
func TestNew(t *testing.T) {
	// Restored by the test framework when the test ends, so the
	// suite's own TMPDIR survives New overwriting it.
	t.Setenv("TMPDIR", t.TempDir())

	root, cleanup, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(cleanup)

	if fi, statErr := os.Stat(root); statErr != nil || !fi.IsDir() {
		t.Fatalf("Stat(%q) = %v, %v; want an existing directory", root, fi, statErr)
	}
	if !isRunRoot(filepath.Base(root)) {
		t.Errorf("root = %q, want the %q prefix so Sweep can recognise it", root, prefix)
	}

	// TMPDIR is the whole mechanism: os.TempDir re-reads it, so
	// every existing CreateTemp("") site follows without changing.
	if got := os.Getenv("TMPDIR"); got != root {
		t.Errorf("TMPDIR = %q, want %q", got, root)
	}
	// The empty dir argument is the behaviour under test: every
	// existing call site passes "" and must follow TMPDIR without
	// being changed. Substituting t.TempDir() here would assert
	// nothing.
	f, err := os.CreateTemp("", "probe-*") //nolint:usetesting // see above
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if !strings.HasPrefix(f.Name(), root) {
		t.Errorf("CreateTemp(\"\") = %q, want it inside %q", f.Name(), root)
	}
}

// TestNewIsUniquePerCall pins that the root carries no fixed path
// component. Two concurrent runs are two processes, so this stands
// in for them: a shared name anywhere in the construction would
// collide here too.
func TestNewIsUniquePerCall(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	a, ca, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(ca)
	b, cb, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(cb)

	if a == b {
		t.Fatalf("both runs got %q, want distinct roots", a)
	}
}

// TestNewCleanupRemovesEverything pins that the cleanup reclaims
// the subtree, not just the empty directory — subprocess leftovers
// are what fills it.
func TestNewCleanupRemovesEverything(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	root, cleanup, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Guard before writing: a root of "" would make the MkdirAll
	// below land in the package directory instead.
	if !filepath.IsAbs(root) {
		t.Fatalf("root = %q, want an absolute path", root)
	}
	nested := filepath.Join(root, "gobco-1234", "deep")
	if mkErr := os.MkdirAll(nested, 0o700); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	if wErr := os.WriteFile(filepath.Join(nested, "copy.go"), []byte("x"), 0o600); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}

	cleanup()

	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Errorf("Stat after cleanup = %v, want the root gone", statErr)
	}
}

// TestRemoveAllUnwritableDir pins cleanup against the tree that
// actually defeated it.
//
// ergon's TestScaffoldWriteFailure chmods a directory to 0 to prove
// the scaffold reports a write error. gremlins copies the module
// into a workdir under the run root during mutation testing, so
// that directory ends up inside the very tree cleanup must remove
// — and os.RemoveAll cannot descend into it.
func TestRemoveAllUnwritableDir(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "run")
	sealed := filepath.Join(root, "gremlins-1", "wd-1", "sealed")
	if err := os.MkdirAll(sealed, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sealed, "f.go"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })

	if err := os.RemoveAll(root); err == nil {
		t.Skip("os.RemoveAll handled the sealed directory; nothing to guard on this platform")
	}
	if err := removeAll(root); err != nil {
		t.Fatalf("removeAll: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("Stat = %v, want the root gone", err)
	}
}

// TestSweep pins the reclamation of roots left by killed runs.
//
// A cleanup deferred in Execute covers a normal exit and SIGINT,
// but not SIGKILL — and 1,749 leftovers on one developer machine
// say that path is taken often. Sweep must reclaim them without
// touching a root a concurrent run is still using, which it cannot
// detect directly: age is the only portable signal.
func TestSweep(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	mk := func(name string, age time.Duration) string {
		t.Helper()
		p := filepath.Join(parent, name)
		if err := os.MkdirAll(filepath.Join(p, "nested"), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return p
	}

	stale := mk(prefix+"111", 48*time.Hour)
	fresh := mk(prefix+"222", time.Minute)
	mine := mk(prefix+"333", 48*time.Hour)
	other := mk("something-else", 48*time.Hour)

	n, err := Sweep(parent, mine, StaleAfter)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("removed %d, want 1", n)
	}
	if _, statErr := os.Stat(stale); !os.IsNotExist(statErr) {
		t.Error("stale root survived, want it reclaimed")
	}
	for name, p := range map[string]string{
		"a root younger than the threshold (a live run)": fresh,
		"the caller's own root":                          mine,
		"an unrelated directory":                         other,
	} {
		if _, statErr := os.Stat(p); statErr != nil {
			t.Errorf("%s was removed, want it left alone", name)
		}
	}
}

// TestSweepMissingParent covers the ordinary case of a machine with
// no leftovers at all: absent parent is not an error.
func TestSweepMissingParent(t *testing.T) {
	t.Parallel()

	n, err := Sweep(filepath.Join(t.TempDir(), "absent"), "", StaleAfter)
	if err != nil {
		t.Errorf("Sweep on a missing parent = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("removed %d, want 0", n)
	}
}
