// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// Inputs bundles the resolved discovery results the test runner
// needs. The caller (cobra layer) populates Root and Modules from
// `discover.Resolve`; CoverageDir is the absolute path where
// per-module coverage profiles are written.
type Inputs struct {
	// Root is the absolute repository root.
	Root string

	// Modules is the per-module iteration set.
	Modules []modules.Module

	// CoverageDir is the absolute directory where per-module
	// coverage profiles are written. The caller is responsible for
	// composing the path (typically `<root>/.<project>/coverage`).
	// Empty disables coverage collection.
	CoverageDir string
}

// Run is the body of `ergon test`: per module, runs `go test ./...`
// with the standard knobs (cpu, count, timeout) and writes a
// per-module coverage profile when CoverageDir is set.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, cfg Config,
) error {
	if in.CoverageDir != "" {
		if err := os.MkdirAll(in.CoverageDir, 0o700); err != nil {
			return fmt.Errorf("create coverage dir: %w", err)
		}
	}
	return modules.Iterate(ctx, in.Modules, func(ctx context.Context, m modules.Module) error {
		args := []string{
			"test",
			"-covermode=atomic",
			"-cpu=" + strconv.Itoa(cfg.CPU),
			"-count=" + strconv.Itoa(cfg.Count),
			"-timeout=" + cfg.Timeout.String(),
		}
		if in.CoverageDir != "" {
			args = append(args, "-coverprofile="+coverageFile(in.CoverageDir, m))
		}
		args = append(args, "./...")
		fmt.Fprintf(stdout, "[%s] go test ./...\n", m.Dir)
		return xexec.RunAllowNoPackages(ctx, runner,
			optsFor(in.Root, m, stdout, stderr), stdout, m.Dir, "go", args...)
	})
}

// Race runs `go test -race ./...` per module with the configured
// race-count and timeout. Coverage is not collected — race mode
// rebuilds the runtime with sanity checks and is too slow to pair
// with the standard coverage run. A module whose packages are all
// gated out by build tags is skipped with a notice.
func Race(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, cfg Config,
) error {
	return modules.Iterate(ctx, in.Modules, func(ctx context.Context, m modules.Module) error {
		args := []string{
			"test",
			"-race",
			"-count=" + strconv.Itoa(cfg.RaceCount),
			"-timeout=" + cfg.Timeout.String(),
			"./...",
		}
		fmt.Fprintf(stdout, "[%s] go test -race ./...\n", m.Dir)
		return xexec.RunAllowNoPackages(ctx, runner,
			optsFor(in.Root, m, stdout, stderr), stdout, m.Dir, "go", args...)
	})
}

// Bench runs the benchmarks in every module:
// `go test -bench=. -run=^$ -benchmem -timeout=...`. The `-run=^$`
// pattern excludes regular tests so the bench output is not mixed
// with test results. A module whose packages are all gated out by
// build tags is skipped with a notice.
func Bench(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, cfg Config,
) error {
	return modules.Iterate(ctx, in.Modules, func(ctx context.Context, m modules.Module) error {
		args := []string{
			"test",
			"-bench=.",
			"-run=^$",
			"-benchmem",
			"-timeout=" + cfg.Timeout.String(),
			"./...",
		}
		fmt.Fprintf(stdout, "[%s] go test -bench=.\n", m.Dir)
		return xexec.RunAllowNoPackages(ctx, runner,
			optsFor(in.Root, m, stdout, stderr), stdout, m.Dir, "go", args...)
	})
}

// optsFor returns the [xexec.Options] for invoking `go test`
// inside module m: cwd is the module's absolute path, output is
// streamed to the caller's writers.
func optsFor(root string, m modules.Module, stdout, stderr io.Writer) xexec.Options {
	return xexec.Options{
		Dir:    filepath.Join(root, m.Dir),
		Stdout: stdout,
		Stderr: stderr,
	}
}

// coverageFile returns the absolute path of the .out coverage
// profile for module m under coverageDir. Module dirs with slashes
// flatten to underscores so the resulting filename is portable.
func coverageFile(coverageDir string, m modules.Module) string {
	name := m.Dir
	if name == "." {
		name = "root"
	}
	name = strings.ReplaceAll(name, "/", "_")
	return filepath.Join(coverageDir, name+".out")
}
