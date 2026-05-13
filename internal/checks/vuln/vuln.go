// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package vuln wraps `govulncheck` for `ergon check vuln`. The
// package owns no configuration — the tool reads its own
// vulnerability database; ergon just runs it once per module.
package vuln

import (
	"context"
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
// When opts.Fast is true the run aborts at the first per-module
// failure; otherwise every module runs and a single summary block
// closes the stage. opts.Verbose streams the raw govulncheck
// output instead of capturing it for failure-only display.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module, opts stage.Options,
) error {
	return stage.PerModule(ctx, stdout, mods, opts,
		"govulncheck", "known-vulnerability scan",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			return stage.RunAllowSkip(ctx, runner, opts,
				filepath.Join(root, m.Dir), m.Dir,
				stdout, stderr, stdout, "govulncheck", "./...")
		})
}
