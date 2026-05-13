// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package release implements `ergon release`: bumps module versions
// across a multi-module repository and creates annotated git tags
// following the Go multi-module convention (root → `v1.2.3`,
// submodule `foo/bar` → `foo/bar/v1.2.3`).
//
// # Bump-level precedence
//
// Highest precedence first:
//
//  1. `--bump MODULE=LEVEL` per-module override (repeatable).
//  2. `--major` / `--minor` / `--patch` global force
//     (mutually exclusive).
//  3. Conventional-commit inference scoped to each module's git
//     history since its last tag: `feat!:` / `<type>!:` /
//     `BREAKING CHANGE` in body → major, `feat:` → minor, anything
//     else → patch, no commits in scope → skip the module.
//
// # First run
//
// A module with no prior tag treats its current version as
// `0.0.0`. Pass `--major` / `--minor` / `--patch` on the first
// run to release at a different version; otherwise the inferred
// level applies to `0.0.0` (so a `feat:` history produces
// `0.1.0`).
package release

import (
	"context"
	"errors"
	"fmt"
	"io"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// Run is `ergon release`'s entry point. Builds the [PlanEntry]
// list for mods, prints it, and (unless opts.DryRun is set) calls
// [ApplyPlan] to create the annotated tags. The caller arranges
// for the runner, repository root, and module set; this function
// owns the orchestration.
//
// Returns an error wrapping [ErrUsage] when the plan cannot be
// computed because of an input fault, or wrapping the underlying
// git error when tag creation fails.
func Run(
	ctx context.Context, runner xexec.Runner, stdout io.Writer,
	root string, mods []modules.Module, opts Options,
) error {
	if len(mods) == 0 {
		return errors.New("no modules to release")
	}
	plan, err := BuildPlan(ctx, runner, root, mods, opts)
	if err != nil {
		return err
	}
	printPlan(stdout, plan)
	if opts.DryRun {
		fmt.Fprintln(stdout, "\n(dry-run; no files changed, no tags created)")
		return nil
	}
	return ApplyPlan(ctx, runner, root, stdout, plan, opts)
}
