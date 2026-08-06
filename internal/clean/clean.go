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

	"go.thesmos.sh/ergon/internal/config"
)

// Canonical directory names ergon writes to during a build /
// release cycle.
const (
	binDir  = "bin"
	distDir = "dist"
)

// Targets returns the repo-relative paths `ergon clean` removes:
// [binDir], ergon's artefact directory ([config.ArtifactDir]), and
// the goreleaser output ([distDir]).
//
// The artefact directory was once derived from the project's name,
// which meant `ergon clean` in a repository called eidos deleted
// `.eidos/` — that project's own runtime state, not ergon's.
// Removal is now confined to a directory ergon alone writes. A
// legacy `.<project>/` left behind by an earlier version is
// deliberately not swept: reaching into a directory ergon does not
// own is the defect, not the remedy.
func Targets() []string {
	return []string{
		binDir,
		config.ArtifactDir,
		distDir,
	}
}

// Run removes every entry in [Targets] relative to root. Missing
// targets are silently skipped — clean is idempotent.
func Run(stdout io.Writer, root string) error {
	for _, t := range Targets() {
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
