// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package vuln wraps `govulncheck` for `ergon check vuln`. The
// package owns no configuration — the tool reads its own
// vulnerability database; ergon just runs it once per module.
package vuln

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// Run executes `govulncheck ./...` in each module. The tool exits
// non-zero on any reachable vulnerability. A module whose packages
// are all gated out by build tags is recorded as skipped rather
// than failing the run.
//
// When fast is true the run aborts at the first per-module
// failure; otherwise every module runs and a single summary block
// closes the stage with one verdict line per module.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module, fast bool,
) error {
	return stage.PerModule(ctx, stdout, mods, fast,
		"govulncheck", "known-vulnerability scan",
		func(ctx context.Context, m modules.Module) (bool, error) {
			opts := xexec.Options{
				Dir:    filepath.Join(root, m.Dir),
				Stdout: stdout,
				Stderr: stderr,
			}
			fmt.Fprintf(stdout, "[%s] govulncheck ./...\n", m.Dir)
			return stage.RunAllowSkip(ctx, runner, opts, stdout, m.Dir,
				"govulncheck", "./...")
		})
}
