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
)

// Run executes `go build ./...` in each discovered module. The
// command surfaces every package-compile error; an unbuildable
// module aborts the run with the offending dir in the wrapper.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module,
) error {
	return modules.Iterate(ctx, mods, func(ctx context.Context, m modules.Module) error {
		opts := xexec.Options{
			Dir:    filepath.Join(root, m.Dir),
			Stdout: stdout,
			Stderr: stderr,
		}
		fmt.Fprintf(stdout, "[%s] go build ./...\n", m.Dir)
		return runner.Run(ctx, opts, "go", "build", "./...")
	})
}
