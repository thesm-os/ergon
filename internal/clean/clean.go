// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package clean implements `ergon clean`: removes the build and
// coverage artefacts ergon's other commands write. Does not touch
// the Go module cache or the test cache — those belong to `go
// clean`, not ergon's lifecycle.
package clean

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Canonical directory names ergon writes to during a build /
// release cycle.
const (
	binDir  = "bin"
	distDir = "dist"
)

// Targets returns the repo-relative paths `ergon clean` removes:
// [binDir], the project's per-name artefact directory (e.g.
// `.ergon/`), and the goreleaser output ([distDir]). The list is
// a function rather than a constant so callers see the exact
// paths even when name varies between repos.
func Targets(name string) []string {
	return []string{
		binDir,
		"." + name,
		distDir,
	}
}

// Run removes every entry in [Targets] relative to root. Missing
// targets are silently skipped — clean is idempotent.
func Run(stdout io.Writer, root, name string) error {
	for _, t := range Targets(name) {
		full := filepath.Join(root, t)
		err := os.RemoveAll(full)
		switch {
		case err == nil:
			fmt.Fprintf(stdout, "removed %s\n", t)
		case errors.Is(err, fs.ErrNotExist):
			// Already gone; idempotent.
		default:
			return fmt.Errorf("clean: remove %s: %w", t, err)
		}
	}
	return nil
}
