// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/test"
)

// ErrBaselineMissing reports that `ergon bench regression` cannot
// run because the pinned baseline file does not exist. The error
// names the path the caller expected so the user can either run
// `ergon bench baseline` or fix the configured path.
var ErrBaselineMissing = errors.New("bench: baseline file missing")

// Baseline runs `go test -bench=. -run=^$ -benchmem -count=N` per
// module and writes the concatenated output to cfg.BaselinePath
// (relative to root). The file becomes the reference subsequent
// `ergon bench regression` runs compare against.
//
// The output is also streamed to stdout so the user sees the
// run in real time.
func Baseline(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module, testCfg test.Config, cfg Config,
) error {
	cfg = withDefaults(cfg)
	fullPath := filepath.Join(root, cfg.BaselinePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		return fmt.Errorf("bench: create baseline dir: %w", err)
	}
	f, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("bench: create %s: %w", cfg.BaselinePath, err)
	}
	defer f.Close()

	sink := io.MultiWriter(f, stdout)
	if err := runBench(ctx, runner, sink, stderr, root, mods, testCfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nBaseline recorded at %s\n", cfg.BaselinePath)
	return nil
}

// Regression runs the bench suite into a temp file and invokes
// `benchstat` to compare it against cfg.BaselinePath. The
// benchstat output is streamed verbatim to stdout; ergon reports
// the comparison but does not enforce a numeric threshold (that
// belongs to `ergon check bench-regression` when wired).
func Regression(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module, testCfg test.Config, cfg Config,
) error {
	cfg = withDefaults(cfg)
	fullBaseline := filepath.Join(root, cfg.BaselinePath)
	if _, err := os.Stat(fullBaseline); err != nil {
		return fmt.Errorf("%w: %s — run `ergon bench baseline` first", ErrBaselineMissing, cfg.BaselinePath)
	}

	tmp, err := os.CreateTemp("", "ergon-bench-current-*.txt")
	if err != nil {
		return fmt.Errorf("bench: create temp: %w", err)
	}
	defer os.Remove(tmp.Name())

	sink := io.MultiWriter(tmp, stdout)
	if err := runBench(ctx, runner, sink, stderr, root, mods, testCfg); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("bench: close temp: %w", err)
	}

	fmt.Fprintln(stdout, "\nComparing against baseline...")
	return runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: stdout, Stderr: stderr},
		"benchstat", fullBaseline, tmp.Name())
}

// withDefaults fills any zero-value field on cfg from [Defaults].
func withDefaults(cfg Config) Config {
	d := Defaults()
	if cfg.BaselinePath == "" {
		cfg.BaselinePath = d.BaselinePath
	}
	return cfg
}

// runBench runs the standard bench invocation
// (`go test -bench=. -run=^$ -benchmem -count=N -timeout=T ./...`)
// in each module. Output goes to sink; the caller composes sink
// from a file plus stdout via io.MultiWriter when both are
// required.
func runBench(
	ctx context.Context, runner xexec.Runner, sink, stderr io.Writer,
	root string, mods []modules.Module, testCfg test.Config,
) error {
	for _, m := range mods {
		cwd := filepath.Join(root, m.Dir)
		args := []string{
			"test",
			"-bench=.",
			"-run=^$",
			"-benchmem",
			"-count=" + strconv.Itoa(testCfg.BenchCount),
			"-timeout=" + testCfg.Timeout.String(),
			"./...",
		}
		err := runner.Run(ctx,
			xexec.Options{Dir: cwd, Stdout: sink, Stderr: stderr},
			"go", args...)
		if err != nil {
			return fmt.Errorf("[%s] go test -bench: %w", m.Dir, err)
		}
	}
	return nil
}
