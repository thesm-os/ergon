// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package generate implements `ergon generate`: runs `go generate
// ./...` per module to regenerate code from `//go:generate`
// directives, then runs the format pipeline so the freshly-emitted
// source matches the repository's style.
package generate

import (
	"context"
	"io"
	"path/filepath"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/format"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// Run regenerates every `//go:generate` directive in the workspace
// and then calls [format.Run] so the new files are formatted and
// the SPDX headers applied.
//
// The generated-code refresh runs as a per-module section; format
// then renders its own three sections (license, go fmt,
// markdownlint) on top.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in format.Inputs, licenseCfg license.Config, markdownCfg markdown.Config,
	opts stage.Options,
) error {
	err := stage.PerModule(ctx, stdout, in.Modules, opts,
		"go generate", "regenerate //go:generate directives",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			return stage.RunAllowSkip(ctx, runner, opts,
				filepath.Join(in.Root, m.Dir), m.Dir,
				stdout, stderr, stdout, "go", "generate", "./...")
		})
	if err != nil {
		return err
	}
	return format.Run(ctx, runner, stdout, stderr, in, licenseCfg, markdownCfg, opts)
}
