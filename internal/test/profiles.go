// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package test

import (
	"fmt"
	"os"
	"path/filepath"
)

// publishInto copies every file in from into to, replacing each
// destination atomically.
//
// Coverage artefacts — the per-module profiles and the HTML reports
// rendered from them — are written to a staging directory under the
// run's private temp root and published here, rather than written
// into the repository directly, because the destination path is
// fixed: `<root>/.<project>/coverage`. Two concurrent runs therefore
// aim `go test -coverprofile` (and `go tool cover -o`) at the same
// files, and a reader — the gate parsing them, a `--no-test`
// re-run, or a browser open on the report — can observe a file
// another process is midway through writing. Measured on the
// pre-fix binary, seven of nine concurrent readers did not merely
// fail: they read a truncated set and reported a coverage verdict
// that was wrong.
//
// The directory cannot simply move under the temp root: it is a
// cache, not scratch. `ergon check coverage --no-test` reuses the
// previous run's profiles, which is the whole point of that flag,
// and the temp root is removed when the run that made it exits.
//
// Atomicity is per file, not per set. Two runs finishing at once can
// still leave a directory holding one module's profile from each,
// which is a coherent file from a coherent run in every case, but
// not one consistent snapshot. Making the set atomic needs a
// directory swap, whose own replace window is not obviously smaller
// than the one it closes.
func publishInto(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return fmt.Errorf("test: read staged coverage in %s: %w", from, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(from, e.Name()))
		if readErr != nil {
			return fmt.Errorf("test: read staged profile %s: %w", e.Name(), readErr)
		}
		if err := replaceFile(filepath.Join(to, e.Name()), body); err != nil {
			return err
		}
	}
	return nil
}

// replaceFile writes body to path so a concurrent reader sees either
// the old contents or the new ones, never a partial write.
//
// The temporary file is created in the destination directory, not
// the run's temp root: rename is only atomic within a filesystem,
// and TMPDIR routinely sits on a different one from the repository.
func replaceFile(path string, body []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("test: stage %s: %w", path, err)
	}
	tmp := f.Name()
	// Every failure past this point leaves a dotfile in the
	// coverage directory otherwise; the gate globs `*.out`, so it
	// would not be read, but it would accumulate silently.
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("test: write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("test: close %s: %w", tmp, err)
	}
	// CreateTemp makes 0600; the profiles the run replaces are
	// 0644-ish from `go test`, and a stricter mode is fine for a
	// build artefact read only by this user.
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("test: publish %s: %w", path, err)
	}
	return nil
}
