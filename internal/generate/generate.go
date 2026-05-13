// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package generate implements `ergon generate`: runs `go generate
// ./...` per module to regenerate code from `//go:generate`
// directives, then runs the format pipeline so the freshly-emitted
// source matches the repository's style.
package generate

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/format"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/modules"
)

// Run regenerates every `//go:generate` directive in the workspace
// and then calls [format.Run] so the new files are formatted and
// the SPDX headers applied.
//
// The generated-code refresh runs per module; format runs once
// across the workspace.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in format.Inputs, licenseCfg license.Config, markdownCfg markdown.Config,
) error {
	err := modules.Iterate(ctx, in.Modules, func(ctx context.Context, m modules.Module) error {
		opts := xexec.Options{
			Dir:    filepath.Join(in.Root, m.Dir),
			Stdout: stdout,
			Stderr: stderr,
		}
		fmt.Fprintf(stdout, "[%s] go generate ./...\n", m.Dir)
		return xexec.RunAllowNoPackages(ctx, runner, opts, stdout, m.Dir,
			"go", "generate", "./...")
	})
	if err != nil {
		return err
	}
	return format.Run(ctx, runner, stdout, stderr, in, licenseCfg, markdownCfg)
}
