// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package format implements `ergon fmt`: applies SPDX license
// headers, runs gofumpt + gci per module to format Go sources,
// then runs markdownlint-cli2 across the workspace's Markdown
// files. The orchestration mirrors what the Makefile templates
// converged on.
//
// Each underlying tool lives in its own subsystem package
// ([license], [markdown]); format only sequences them.
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
// failures are surfaced as warnings on stderr — fmt is the write
// path; `ergon lint` is where Markdown errors block.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, licenseCfg license.Config, markdownCfg markdown.Config,
) error {
	if err := license.Apply(ctx, runner, stdout, stderr, in.Root, licenseCfg); err != nil {
		return fmt.Errorf("license: %w", err)
	}

	err := modules.Iterate(ctx, in.Modules, func(ctx context.Context, m modules.Module) error {
		opts := xexec.Options{
			Dir:    filepath.Join(in.Root, m.Dir),
			Stdout: stdout,
			Stderr: stderr,
		}
		fmt.Fprintf(stdout, "[%s] gofumpt\n", m.Dir)
		if err := runner.Run(ctx, opts, "gofumpt", "-l", "-w", "-extra", "."); err != nil {
			return fmt.Errorf("gofumpt: %w", err)
		}
		fmt.Fprintf(stdout, "[%s] gci\n", m.Dir)
		if err := runner.Run(ctx, opts,
			"gci", "write",
			"--section", "standard",
			"--section", "default",
			"--section", "prefix("+in.ImportPath+")",
			"--custom-order",
			"--skip-generated",
			".",
		); err != nil {
			return fmt.Errorf("gci: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	return markdown.Format(ctx, runner, stdout, stderr, in.Root, markdownCfg)
}
