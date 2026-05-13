// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package format implements `ergon fmt`: applies SPDX license
// headers, runs gofumpt + gci per module to format Go sources,
// then runs markdownlint-cli2 across the workspace's Markdown
// files. The orchestration mirrors what the Makefile templates
// converged on.
//
// Each underlying tool lives in its own subsystem package
// ([license], [markdown]); format only sequences them. Every
// stage renders through [stage.Single] / [stage.PerModule] so the
// report looks consistent with `ergon check` and `ergon lint`.
package format

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// Inputs bundles the resolved discovery results [Run] needs. The
// caller (cobra layer) populates it from `discover.Resolve` and
// `discover.ImportPath` so format does not pull in those concerns
// itself.
type Inputs struct {
	// Root is the absolute repository root.
	Root string

	// ImportPath is the root module's import path, used to compose
	// the `gci` prefix section.
	ImportPath string

	// Modules is the per-module iteration set.
	Modules []modules.Module
}

// Run is the body of `ergon fmt`: applies SPDX license headers
// across the repository, runs gofumpt and gci per module, then
// runs markdownlint-cli2 (auto-fix mode) across the workspace.
//
// License application failures abort the run (headers determine
// downstream lint behaviour). gofumpt and gci failures abort the
// run (a botched format is a real problem). Markdown lint
// failures still abort the run too — fmt is the write path, and
// a markdownlint failure here usually means the tool itself is
// missing or misconfigured rather than a Markdown finding.
//
// opts controls how the staged output renders: opts.Verbose
// streams the underlying tool output live, the default buffers
// each call and reveals it indented under the failing verdict
// when needed.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, licenseCfg license.Config, markdownCfg markdown.Config,
	opts stage.Options,
) error {
	if err := stage.Single(ctx, stdout, opts,
		"go-license", "apply SPDX license headers",
		"every source file carries the expected SPDX header", "",
		func(_ context.Context, sOut, sErr io.Writer) error {
			return license.Apply(ctx, runner, sOut, sErr, in.Root, licenseCfg)
		}); err != nil {
		return err
	}

	if err := stage.PerModule(ctx, stdout, in.Modules, opts,
		"go fmt", "gofumpt + gci",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			return runGoFmt(ctx, runner, opts,
				filepath.Join(in.Root, m.Dir), m.Dir, in.ImportPath,
				stdout, stderr)
		}); err != nil {
		return err
	}

	return stage.Single(ctx, stdout, opts,
		"markdownlint", "Markdown auto-fix",
		"every Markdown file fixed in place", "",
		func(_ context.Context, sOut, sErr io.Writer) error {
			return markdown.Format(ctx, runner, sOut, sErr, in.Root, markdownCfg)
		})
}

// runGoFmt runs gofumpt then gci against one module. Both tools
// flow through [stage.RunAllowSkip] so a module whose packages
// are all gated by build tags is recorded as skipped, and so
// tool output is buffered + revealed on failure consistently
// with every other ergon gate.
func runGoFmt(
	ctx context.Context, runner xexec.Runner, opts stage.Options,
	dir, label, importPath string, stdout, stderr io.Writer,
) stage.StepResult {
	r := stage.RunAllowSkip(ctx, runner, opts, dir, label,
		stdout, stderr, stdout,
		"gofumpt", "-l", "-w", "-extra", ".")
	if r.Err != nil || r.Skipped {
		return r
	}
	r = stage.RunAllowSkip(ctx, runner, opts, dir, label,
		stdout, stderr, stdout,
		"gci", "write",
		"--section", "standard",
		"--section", "default",
		"--section", "prefix("+importPath+")",
		"--custom-order",
		"--skip-generated",
		".")
	if r.Err != nil {
		// Surface the originating tool in the error chain so the
		// rendered verdict line says "gci: ..." rather than a bare
		// exit-status message.
		r.Err = fmt.Errorf("gci: %w", r.Err)
	}
	return r
}
