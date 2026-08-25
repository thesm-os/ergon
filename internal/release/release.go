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
		return errors.New("release: no modules to release")
	}
	plan, err := BuildPlan(ctx, runner, root, mods, opts)
	if err != nil {
		return err
	}
	// A module whose requires this release rewrites is released
	// too, so the rewrite reaches consumers instead of sitting on
	// main under a tag that predates it. Runs before annotation,
	// since promotion changes which entries are skipped.
	if !opts.NoBump && !opts.NoCascade {
		plan, err = cascadeDependents(root, plan)
		if err != nil {
			return err
		}
	}
	// Annotated before printing, so the plan names every file the
	// run will write. Read-only, so --dry-run stays a dry run.
	if !opts.NoBump {
		plan, err = annotatePins(root, plan)
		if err != nil {
			return err
		}
	}
	// A cycle is reported, not returned: the plan below is exactly
	// what an operator needs in order to see the problem, and
	// swallowing it to raise an error would hide the listing that
	// names the modules involved. The layered pipeline aborts on its
	// own further down; --version routes around it entirely.
	waves, wavesErr := planWaves(root, mods, plan)
	var cycle *CycleError
	if wavesErr != nil && !errors.As(wavesErr, &cycle) {
		return wavesErr
	}
	// --version releases every module at once, so the layering
	// computed above is not what will happen. Rendering it anyway
	// promised waves and per-wave pushes the apply never performs —
	// a plan that describes a different pipeline is worse than one
	// that describes none.
	if opts.Version != "" {
		waves, cycle = [][]PlanEntry{plan}, nil
	}
	printPlan(stdout, waves)
	if opts.Version != "" {
		fmt.Fprintf(stdout,
			"\n  Single wave: every module at %s — pin, tidy, "+
				"one commit, %d tag(s), one push.\n",
			opts.Version, len(taggable(plan)))
	}
	if cycle != nil {
		fmt.Fprintf(stdout, "\n  ! %s\n", cycle.Error())
	}
	if opts.DryRun {
		fmt.Fprintln(stdout, "\n(dry-run; no files changed, no tags created)")
		return nil
	}
	return ApplyPipeline(ctx, runner, root, stdout, mods, plan, opts)
}
