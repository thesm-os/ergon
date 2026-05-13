// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package lint implements `ergon lint` and its subcommands. The
// package owns the Go-specific lint steps (vet, golangci-lint) and
// orchestrates the four-stage default suite — vet, golangci-lint,
// markdown lint, license verify.
//
// Markdown and license reuse the entry points from
// [go.thesmos.sh/ergon/internal/markdown] and
// [go.thesmos.sh/ergon/internal/license] so format and lint share
// a single implementation per tool.
package lint

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

// Inputs bundles the resolved discovery results [All] and its
// per-step helpers need.
type Inputs struct {
	// Root is the absolute repository root.
	Root string

	// Modules is the per-module iteration set.
	Modules []modules.Module
}

// All runs the four lint stages in order: [Vet], [Go],
// markdown.Lint, license.Verify. Returns the first error
// encountered; lint is a CI gate, so any failure should be
// surfaced as a hard fail.
func All(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, markdownCfg markdown.Config, licenseCfg license.Config,
) error {
	if err := Vet(ctx, runner, stdout, stderr, in); err != nil {
		return err
	}
	if err := Go(ctx, runner, stdout, stderr, in); err != nil {
		return err
	}
	if err := markdown.Lint(ctx, runner, stdout, stderr, in.Root, markdownCfg); err != nil {
		return fmt.Errorf("markdown: %w", err)
	}
	if err := license.Verify(ctx, runner, stdout, stderr, in.Root, licenseCfg); err != nil {
		return fmt.Errorf("license: %w", err)
	}
	return nil
}

// Vet runs `go vet ./...` per module. The stdlib analyser catches
// the well-known correctness issues; failure is per-module so the
// caller can identify which module's source surfaced the finding.
func Vet(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs,
) error {
	return modules.Iterate(ctx, in.Modules, func(ctx context.Context, m modules.Module) error {
		opts := xexec.Options{
			Dir:    filepath.Join(in.Root, m.Dir),
			Stdout: stdout,
			Stderr: stderr,
		}
		fmt.Fprintf(stdout, "[%s] go vet ./...\n", m.Dir)
		return runner.Run(ctx, opts, "go", "vet", "./...")
	})
}

// Go runs `golangci-lint run ./...` per module. The linter reads
// its own `.golangci.yml` for which checks to enable and any
// runtime tuning (timeout, etc.) — ergon passes no flags so users
// configure golangci-lint through its native surface.
func Go(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs,
) error {
	return modules.Iterate(ctx, in.Modules, func(ctx context.Context, m modules.Module) error {
		opts := xexec.Options{
			Dir:    filepath.Join(in.Root, m.Dir),
			Stdout: stdout,
			Stderr: stderr,
		}
		fmt.Fprintf(stdout, "[%s] golangci-lint run\n", m.Dir)
		return runner.Run(ctx, opts, "golangci-lint", "run", "./...")
	})
}
