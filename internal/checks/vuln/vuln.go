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
)

// Run executes `govulncheck ./...` in each module. The tool exits
// non-zero on any reachable vulnerability; first failure aborts the
// run with the offending module's directory in the wrapper. A
// module whose packages are all gated out by build tags is skipped
// with a notice rather than failing the run.
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
		fmt.Fprintf(stdout, "[%s] govulncheck ./...\n", m.Dir)
		return xexec.RunAllowNoPackages(ctx, runner, opts, stdout, m.Dir,
			"govulncheck", "./...")
	})
}
