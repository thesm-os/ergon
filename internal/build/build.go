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
// When opts.Fast is true the run aborts at the first per-module
// failure; otherwise every module runs and a single summary block
// closes the stage with one verdict line per module.
// opts.Verbose streams the raw compile output instead of buffering.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module, opts stage.Options,
) error {
	return stage.PerModule(ctx, stdout, mods, opts,
		"go build", "compile every package",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			return stage.RunAllowSkip(ctx, runner, opts,
				filepath.Join(root, m.Dir), m.Dir,
				stdout, stderr, stdout, "go", "build", "./...")
		})
}
