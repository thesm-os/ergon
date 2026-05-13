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
	"time"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
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
//
// ov shadows fields of cfg for this invocation. Zero values on
// ov leave the configured value in place; ov.Pattern, when set,
// becomes `-run=<pattern>`.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, cfg Config, ov Override, opts stage.Options,
) error {
	if in.CoverageDir != "" {
		if err := os.MkdirAll(in.CoverageDir, 0o700); err != nil {
			return fmt.Errorf("create coverage dir: %w", err)
		}
	}
	count := pickInt(ov.Count, cfg.Count)
	cpu := pickInt(ov.CPU, cfg.CPU)
	timeout := pickDuration(ov.Timeout, cfg.Timeout)

	return stage.PerModule(ctx, stdout, in.Modules, opts,
		"go test", "unit tests + coverage",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			args := []string{
				"test",
				"-covermode=atomic",
				"-cpu=" + strconv.Itoa(cpu),
				"-count=" + strconv.Itoa(count),
				"-timeout=" + timeout.String(),
			}
			args = appendRunFlag(args, ov.Pattern)
			if in.CoverageDir != "" {
				args = append(args, "-coverprofile="+coverageFile(in.CoverageDir, m))
			}
			args = append(args, "./...")
			return stage.RunAllowSkip(ctx, runner, opts,
				filepath.Join(in.Root, m.Dir), m.Dir,
				stdout, stderr, stdout, "go", args...)
		})
}

// Race runs `go test -race ./...` per module with the configured
// race-count and timeout. Coverage is not collected — race mode
// rebuilds the runtime with sanity checks and is too slow to pair
// with the standard coverage run.
func Race(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, cfg Config, ov Override, opts stage.Options,
) error {
	count := pickInt(ov.Count, cfg.RaceCount)
	timeout := pickDuration(ov.Timeout, cfg.Timeout)

	return stage.PerModule(ctx, stdout, in.Modules, opts,
		"go test -race", "race-detector run",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			args := []string{
				"test",
				"-race",
				"-count=" + strconv.Itoa(count),
				"-timeout=" + timeout.String(),
			}
			if cpu := pickInt(ov.CPU, cfg.CPU); cpu > 0 {
				args = append(args, "-cpu="+strconv.Itoa(cpu))
			}
			args = appendRunFlag(args, ov.Pattern)
			args = append(args, "./...")
			return stage.RunAllowSkip(ctx, runner, opts,
				filepath.Join(in.Root, m.Dir), m.Dir,
				stdout, stderr, stdout, "go", args...)
		})
}

// Bench runs the benchmarks in every module:
// `go test -bench=<pattern> -run=^$ -benchmem -timeout=...`. The
// `-run=^$` pattern excludes regular tests so the bench output is
// not mixed with test results.
//
// ov.Pattern overrides the default `-bench` value of `.`;
// ov.Count overrides cfg.BenchCount when non-zero (zero means
// `-count` is omitted entirely, which lets Go pick its default
// of 1); ov.Time, when set, becomes `-benchtime`.
func Bench(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, cfg Config, ov Override, opts stage.Options,
) error {
	benchPattern := "."
	if ov.Pattern != "" {
		benchPattern = ov.Pattern
	}
	timeout := pickDuration(ov.Timeout, cfg.Timeout)
	count := pickInt(ov.Count, cfg.BenchCount)

	return stage.PerModule(ctx, stdout, in.Modules, opts,
		"go test -bench", "benchmark run",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			args := []string{
				"test",
				"-bench=" + benchPattern,
				"-run=^$",
				"-benchmem",
				"-timeout=" + timeout.String(),
			}
			if count > 0 {
				args = append(args, "-count="+strconv.Itoa(count))
			}
			if ov.Time > 0 {
				args = append(args, "-benchtime="+ov.Time.String())
			}
			args = append(args, "./...")
			return stage.RunAllowSkip(ctx, runner, opts,
				filepath.Join(in.Root, m.Dir), m.Dir,
				stdout, stderr, stdout, "go", args...)
		})
}

// optsFor returns the [xexec.Options] for invoking a subprocess
// inside module m: cwd is the module's absolute path, output is
// streamed to the caller's writers. Used by [Fuzz] which does not
// flow through [stage.PerModule].
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

// appendRunFlag appends `-run=<pattern>` to args when pattern is
// non-empty. Centralised so every caller honours the same
// "empty pattern = no -run flag" contract.
func appendRunFlag(args []string, pattern string) []string {
	if pattern == "" {
		return args
	}
	return append(args, "-run="+pattern)
}

// pickInt returns override when non-zero, otherwise fallback.
// Centralises the zero-detection pattern every override field
// uses.
func pickInt(override, fallback int) int {
	if override != 0 {
		return override
	}
	return fallback
}

// pickDuration mirrors [pickInt] for [time.Duration] fields.
func pickDuration(override, fallback time.Duration) time.Duration {
	if override != 0 {
		return override
	}
	return fallback
}
