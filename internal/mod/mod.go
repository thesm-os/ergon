// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package mod implements the multi-module hygiene operations that
// back `ergon mod install`, `ergon mod tidy`, and `ergon mod
// verify`. Each public function takes the repository root and the
// module set to operate on; iteration and per-module error
// wrapping go through [modules.Iterate].
package mod

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// ErrDirty signals that one or more modules' go.mod or go.sum
// changed after `go mod tidy`. The error message names every
// dirty module so the caller can fix them in one pass.
var ErrDirty = errors.New("mod: uncommitted go.mod/go.sum changes after `go mod tidy`")

// Install runs `go mod download` followed by `go mod verify` in
// each module. The pair mirrors what `make install` did in the
// Makefile templates — download fills the module cache, verify
// confirms checksums match `go.sum`.
func Install(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module,
) error {
	return modules.Iterate(ctx, mods, func(ctx context.Context, m modules.Module) error {
		opts := xexec.Options{Dir: filepath.Join(root, m.Dir), Stdout: stdout, Stderr: stderr}
		fmt.Fprintf(stdout, "[%s] go mod download\n", m.Dir)
		if err := runner.Run(ctx, opts, "go", "mod", "download"); err != nil {
			return fmt.Errorf("go mod download: %w", err)
		}
		fmt.Fprintf(stdout, "[%s] go mod verify\n", m.Dir)
		if err := runner.Run(ctx, opts, "go", "mod", "verify"); err != nil {
			return fmt.Errorf("go mod verify: %w", err)
		}
		return nil
	})
}

// Tidy runs `go mod tidy` in each module. Modifies go.mod and
// go.sum on disk per Go's tidy semantics — the caller is
// responsible for committing the result.
func Tidy(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module,
) error {
	return modules.Iterate(ctx, mods, func(ctx context.Context, m modules.Module) error {
		opts := xexec.Options{Dir: filepath.Join(root, m.Dir), Stdout: stdout, Stderr: stderr}
		fmt.Fprintf(stdout, "[%s] go mod tidy\n", m.Dir)
		if err := runner.Run(ctx, opts, "go", "mod", "tidy"); err != nil {
			return fmt.Errorf("go mod tidy: %w", err)
		}
		return nil
	})
}

// Verify runs [Tidy] then checks every module's go.mod and go.sum
// against the working tree via `git diff --quiet`. Any module
// whose tracked files would change is reported as dirty and the
// whole call surfaces an [ErrDirty]-wrapped error listing them.
//
// The intent matches the Makefile's `check-tidy` gate: a clean
// `go mod tidy` is a precondition for merging, so divergence is a
// CI-blocking finding rather than a silent drift.
func Verify(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module,
) error {
	if err := Tidy(ctx, runner, stdout, stderr, root, mods); err != nil {
		return err
	}

	var dirty []string
	for _, m := range mods {
		modGo := filepath.Join(m.Dir, "go.mod")
		sumGo := filepath.Join(m.Dir, "go.sum")
		err := runner.Run(ctx,
			xexec.Options{Dir: root, Stdout: io.Discard, Stderr: io.Discard},
			"git", "diff", "--quiet", "--", modGo, sumGo)
		if err != nil {
			dirty = append(dirty, m.Dir)
			fmt.Fprintf(stderr, "[%s] go.mod or go.sum changed after `go mod tidy`\n", m.Dir)
		}
	}
	if len(dirty) > 0 {
		return fmt.Errorf("%w: %v", ErrDirty, dirty)
	}
	return nil
}
