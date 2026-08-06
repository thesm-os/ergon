// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package runtmp gives each ergon invocation a private temp root.
//
// Every temp file ergon and its subprocesses create lands under one
// directory owned by that invocation, so two concurrent runs cannot
// touch each other's scratch space and the whole lot is reclaimed
// by a single RemoveAll.
//
// The root is published through the environment rather than
// threaded as a parameter. os.TempDir consults those variables on
// every call, so ergon's own os.CreateTemp("") and os.MkdirTemp("")
// sites follow with no signature change — and, more importantly, so
// do the subprocesses. gobco copies an entire module per package
// and gremlins creates a workdir per worker; those are the bulk of
// the footprint and a threaded parameter cannot reach them.
package runtmp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// prefix names every per-run root. Kept distinctive so [Sweep] can
// recognise ergon's own leftovers without a manifest.
const prefix = "ergon-run-"

// tempDirVars are the environment variables os.TempDir consults,
// which differ by platform.
//
// Unix reads TMPDIR. Windows resolves through GetTempPath, which
// checks TMP, then TEMP, then USERPROFILE, and never looks at
// TMPDIR — so setting TMPDIR alone left every temp on Windows in
// the system directory while the run root sat empty, and the
// isolation silently did nothing. TMPDIR is set on Windows too:
// it costs nothing and cross-platform tooling that reads it
// directly then agrees with the Go runtime.
func tempDirVars() []string {
	if runtime.GOOS == "windows" {
		return []string{"TMP", "TEMP", "TMPDIR"}
	}
	return []string{"TMPDIR"}
}

// StaleAfter is how old an abandoned root must be before [Sweep]
// reclaims it.
//
// ergon cannot distinguish a root belonging to a live run from one
// left by a killed run — there is no portable way to ask whether
// another process still holds a directory. Age is the only safe
// signal, and the threshold has to exceed the longest plausible
// run: a full `ergon check` over a large workspace with mutation
// and branch coverage enabled is minutes, not hours.
const StaleAfter = 24 * time.Hour

// New creates this invocation's temp root and points TMPDIR at it.
//
// Returns the root and a cleanup that removes it. Callers invoke
// New once, as early as possible: os.Setenv races concurrent
// os.Environ reads, and the branch gate reads the environment from
// a worker pool.
func New() (string, func(), error) {
	// Created under the inherited TMPDIR before it is overwritten,
	// so the root honours an operator's choice of temp filesystem
	// rather than forcing /tmp.
	root, err := os.MkdirTemp("", prefix+"*")
	if err != nil {
		return "", func() {}, fmt.Errorf("runtmp: create run root: %w", err)
	}
	for _, key := range tempDirVars() {
		if err := os.Setenv(key, root); err != nil {
			_ = os.RemoveAll(root)
			return "", func() {}, fmt.Errorf("runtmp: set %s: %w", key, err)
		}
	}
	return root, func() { _ = removeAll(root) }, nil
}

// removeAll deletes path, restoring traversal permission on any
// directory that refuses to go.
//
// os.RemoveAll cannot descend into a directory without write and
// execute permission, and ergon's own suite creates exactly that:
// TestScaffoldWriteFailure chmods a directory to 0 to prove the
// scaffold surfaces a write error. Under mutation testing gremlins
// copies the module — that directory included — into a workdir
// inside the run root, so the run's own cleanup then fails and
// leaks the entire subtree. Discarding that error is how a
// developer machine accumulated 1,749 abandoned roots.
func removeAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}
	// WalkDir calls the callback for a directory before reading its
	// entries, so chmod here makes the descent possible.
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || !d.IsDir() {
			return nil //nolint:nilerr // best effort; the retry below reports
		}
		// 0700, not 0600: a directory needs the execute bit to be
		// traversable, and without traversal RemoveAll cannot reach
		// the entries it has to unlink. Owner-only, and the tree is
		// unlinked moments later.
		_ = os.Chmod(p, 0o700) //nolint:gosec // G302: directories need +x to be removable
		return nil
	})
	if retryErr := os.RemoveAll(path); retryErr != nil {
		return fmt.Errorf("runtmp: remove %s: %w", path, retryErr)
	}
	return nil
}

// Sweep removes abandoned per-run roots under parent, skipping keep
// and anything newer than [StaleAfter]. Returns how many it
// removed.
//
// keep is the caller's own root: `ergon clean` runs inside an
// invocation that owns one, and deleting it mid-run would pull the
// scratch space out from under the command doing the deleting.
func Sweep(parent, keep string, olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		// A machine with no leftovers may have no parent at all;
		// nothing to reclaim is not a failure to reclaim.
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("runtmp: read %s: %w", parent, err)
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !isRunRoot(e.Name()) {
			continue
		}
		full := filepath.Join(parent, e.Name())
		if full == keep {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			// Raced by another sweep or by the owning run's own
			// cleanup. Either way it is no longer ours to reclaim.
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if rmErr := removeAll(full); rmErr != nil {
			return removed, rmErr
		}
		removed++
	}
	return removed, nil
}

// isRunRoot reports whether name looks like a per-run root rather
// than an unrelated entry that happens to share the directory.
func isRunRoot(name string) bool {
	return strings.HasPrefix(name, prefix)
}
