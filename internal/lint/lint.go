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
//
// Aggregation: when fast is false (the default for CI), every
// per-module / per-substage failure is recorded and rendered into
// a single closing summary block; the run goes to completion and
// the caller sees every problem at once. When fast is true the
// run aborts at the first failure — the dev-loop ergonomic.
package lint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
	"go.thesmos.sh/ergon/internal/style"
)

// Inputs bundles the resolved discovery results [All] and its
// per-step helpers need.
type Inputs struct {
	// Root is the absolute repository root.
	Root string

	// Modules is the per-module iteration set.
	Modules []modules.Module
}

// All runs the lint stages in order: [Vet], [Go], markdown.Lint,
// license.Verify — minus any stages excluded by filter. When
// opts.Fast is false every (filtered-in) stage runs even if an
// earlier one failed; when true the first failure aborts the run.
// The final aggregated error wraps every stage that failed.
//
// filter selects the stages that participate (allow/deny lists
// from `.ergon.yaml`'s `lint` section composed with the
// `--only`/`--skip` CLI flags). An empty filter — the zero value —
// runs every built-in stage, matching the historical behaviour.
// Unknown stage names in the filter surface
// [stage.ErrUnknownStage]; the cobra layer wraps that into a
// usage error.
func All(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, markdownCfg markdown.Config, licenseCfg license.Config,
	filter stage.Filter,
	opts stage.Options,
) error {
	stages := []stage.Named{
		{Name: "vet", Run: func() error { return Vet(ctx, runner, stdout, stderr, in, opts) }},
		{Name: "go", Run: func() error { return Go(ctx, runner, stdout, stderr, in, opts) }},
		{Name: "md", Run: func() error {
			return stage.Single(ctx, stdout, opts,
				"markdownlint", "Markdown style and link checks",
				"every Markdown file passed", "",
				func(_ context.Context, sOut, sErr io.Writer) error {
					return markdown.Lint(ctx, runner, sOut, sErr, in.Root, markdownCfg)
				})
		}},
		{Name: "license", Run: func() error {
			return stage.Single(ctx, stdout, opts,
				"go-license", "SPDX license-header verification",
				"every source file carries the expected SPDX header", "",
				func(_ context.Context, sOut, sErr io.Writer) error {
					return license.Verify(ctx, runner, sOut, sErr, in.Root, licenseCfg)
				})
		}},
	}
	selected, err := filter.Apply(stages)
	if err != nil {
		return err
	}

	s := style.Detect(stdout)
	var failures []error
	for _, st := range selected {
		if runErr := st.Run(); runErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", st.Name, runErr))
			if opts.Fast {
				break
			}
		}
	}
	if len(failures) == 0 {
		return nil
	}
	s.FinalVerdict(stderr, false,
		fmt.Sprintf("%d lint stage(s) failed (see per-stage reports above)", len(failures)))
	return errors.Join(failures...)
}

// Vet runs `go vet ./...` per module. The stdlib analyser catches
// the well-known correctness issues. A module whose packages are
// all gated out by build tags is recorded as skipped rather than
// failing the run.
//
// When opts.Fast is true the run aborts at the first per-module
// failure; otherwise every module runs and a single summary block
// closes the stage. opts.Verbose streams the raw tool output.
func Vet(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, opts stage.Options,
) error {
	return stage.PerModule(ctx, stdout, in.Modules, opts,
		"go vet", "static-analysis checks",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			return stage.RunAllowSkip(ctx, runner, opts,
				filepath.Join(in.Root, m.Dir), m.Dir,
				stdout, stderr, stdout, "go", "vet", "./...")
		})
}

// Go runs `golangci-lint run ./...` per module. The linter reads
// its own `.golangci.yml` for which checks to enable and any
// runtime tuning (timeout, etc.) — ergon passes no flags so users
// configure golangci-lint through its native surface. A module
// whose packages are all gated out by build tags is recorded as
// skipped rather than failing the run.
//
// When opts.Fast is true the run aborts at the first per-module
// failure; otherwise every module runs and a single summary block
// closes the stage. opts.Verbose streams the raw tool output.
func Go(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, opts stage.Options,
) error {
	return stage.PerModule(ctx, stdout, in.Modules, opts,
		"golangci-lint", "configured linters",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			return stage.RunAllowSkip(ctx, runner, opts,
				filepath.Join(in.Root, m.Dir), m.Dir,
				stdout, stderr, stdout, "golangci-lint", "run", "./...")
		})
}
