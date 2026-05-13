// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package build implements `ergon build`: compiles every package
// in every module. Acts as a sanity-check gate — no binaries are
// produced (cmd/* packages get linked but written to a temp
// location Go selects); use `goreleaser` for stamped release
// builds.
package build

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// Run executes `go build ./...` in each discovered module. The
// command surfaces every package-compile error. A module whose
// packages are all gated out by build tags is recorded as skipped
// rather than failing the run.
//
// When fast is true the run aborts at the first per-module
// failure; otherwise every module runs and a single summary block
// closes the stage with one verdict line per module.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module, fast bool,
) error {
	return stage.PerModule(ctx, stdout, mods, fast,
		"go build", "compile every package",
		func(ctx context.Context, m modules.Module) (bool, error) {
			opts := xexec.Options{
				Dir:    filepath.Join(root, m.Dir),
				Stdout: stdout,
				Stderr: stderr,
			}
			fmt.Fprintf(stdout, "[%s] go build ./...\n", m.Dir)
			return stage.RunAllowSkip(ctx, runner, opts, stdout, m.Dir,
				"go", "build", "./...")
		})
}
