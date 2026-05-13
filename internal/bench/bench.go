// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/test"
)

// ErrBaselineMissing reports that `ergon bench regression` cannot
// run because the pinned baseline file does not exist.
var ErrBaselineMissing = errors.New("bench: baseline file missing")

// Baseline runs `go test -bench=. -run=^$ -benchmem -count=N` per
// module, captures the output, and writes it to cfg.BaselinePath
// in the benchstat-compatible text format. When no `Benchmark`
// lines surface in the output (the project has no benchmark
// functions, or none ran), the baseline is NOT written and the
// caller sees a notice — recording an empty file would mislead
// later `ergon bench regression` invocations.
func Baseline(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module, testCfg test.Config, cfg Config,
) error {
	cfg = withDefaults(cfg)

	var captured bytes.Buffer
	sink := io.MultiWriter(stdout, &captured)
	if err := runBench(ctx, runner, sink, stderr, root, mods, testCfg); err != nil {
		return err
	}

	if !hasBenchmarks(captured.String()) {
		fmt.Fprintln(stdout, "\nbench: no Benchmark functions found; baseline not written")
		return nil
	}

	fullPath := filepath.Join(root, cfg.BaselinePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		return fmt.Errorf("bench: create baseline dir: %w", err)
	}
	if err := os.WriteFile(fullPath, captured.Bytes(), 0o600); err != nil {
		return fmt.Errorf("bench: write %s: %w", cfg.BaselinePath, err)
	}
	fmt.Fprintf(stdout, "\nBaseline recorded at %s\n", cfg.BaselinePath)
	return nil
}

// Regression runs the bench suite into a temp file and uses
// `benchstat` to produce both the human-facing diff (text) and a
// machine-readable CSV. The CSV is parsed and any per-benchmark
// per-metric percent change above [Thresholds] surfaces as a
// regression; the command fails when at least one regression is
// reported.
//
// When the fresh run produces no Benchmark lines the command
// short-circuits with a notice — there is nothing to compare.
func Regression(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, mods []modules.Module, testCfg test.Config, cfg Config,
) error {
	cfg = withDefaults(cfg)
	fullBaseline := filepath.Join(root, cfg.BaselinePath)
	if _, err := os.Stat(fullBaseline); err != nil {
		return fmt.Errorf("%w: %s — run `ergon bench baseline` first", ErrBaselineMissing, cfg.BaselinePath)
	}

	var captured bytes.Buffer
	sink := io.MultiWriter(stdout, &captured)
	if err := runBench(ctx, runner, sink, stderr, root, mods, testCfg); err != nil {
		return err
	}
	if !hasBenchmarks(captured.String()) {
		fmt.Fprintln(stdout, "\nbench: no Benchmark functions found; nothing to compare")
		return nil
	}

	tmp, tmpErr := os.CreateTemp("", "ergon-bench-current-*.txt")
	if tmpErr != nil {
		return fmt.Errorf("bench: create temp: %w", tmpErr)
	}
	defer os.Remove(tmp.Name())
	if _, writeErr := tmp.Write(captured.Bytes()); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("bench: write temp: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("bench: close temp: %w", closeErr)
	}

	fmt.Fprintln(stdout, "\nComparing against baseline...")
	if textErr := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: stdout, Stderr: stderr},
		"benchstat", fullBaseline, tmp.Name()); textErr != nil {
		return fmt.Errorf("bench: benchstat (text): %w", textErr)
	}

	var csvBuf bytes.Buffer
	if csvErr := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &csvBuf, Stderr: stderr},
		"benchstat", "-format", "csv", fullBaseline, tmp.Name()); csvErr != nil {
		return fmt.Errorf("bench: benchstat (csv): %w", csvErr)
	}

	results, parseErr := parseBenchstatCSV(csvBuf.String())
	if parseErr != nil {
		return fmt.Errorf("bench: parse benchstat: %w", parseErr)
	}
	outcomes := classify(results, cfg.Thresholds)
	warns := warnings(outcomes)
	fails := failures(outcomes)

	if len(warns) > 0 {
		fmt.Fprintln(stdout, "\nAdvisory (B/op above threshold; not a regression):")
		for _, w := range warns {
			fmt.Fprintf(stdout, "  %s %s: %+.1f%% (threshold %.1f%%)\n",
				w.Result.Bench, w.Result.Metric, w.Result.DeltaPercent, w.Threshold)
		}
	}

	if len(fails) == 0 {
		fmt.Fprintln(stdout, "\nbench: no regressions exceed configured thresholds")
		return nil
	}
	fmt.Fprintln(stderr, "\nRegressions exceed configured thresholds:")
	for _, f := range fails {
		fmt.Fprintf(stderr, "  %s %s: %+.1f%% (threshold %.1f%%)\n",
			f.Result.Bench, f.Result.Metric, f.Result.DeltaPercent, f.Threshold)
	}
	return fmt.Errorf("bench: %d regression(s) exceed threshold", len(fails))
}

// withDefaults fills any zero-value field on cfg from [Defaults].
func withDefaults(cfg Config) Config {
	d := Defaults()
	if cfg.BaselinePath == "" {
		cfg.BaselinePath = d.BaselinePath
	}
	if cfg.Thresholds.TimePercent == 0 {
		cfg.Thresholds.TimePercent = d.Thresholds.TimePercent
	}
	if cfg.Thresholds.BytesPercent == 0 {
		cfg.Thresholds.BytesPercent = d.Thresholds.BytesPercent
	}
	if cfg.Thresholds.AllocsPercent == 0 {
		cfg.Thresholds.AllocsPercent = d.Thresholds.AllocsPercent
	}
	return cfg
}

// runBench runs the standard bench invocation
// (`go test -bench=. -run=^$ -benchmem -count=N -timeout=T ./...`)
// in each module. Output goes to sink; the caller composes sink
// from a buffer plus stdout via io.MultiWriter. A module whose
// packages are all gated out by build tags is skipped with a
// notice rather than failing the run.
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
		err := xexec.RunAllowNoPackages(ctx, runner,
			xexec.Options{Dir: cwd, Stdout: sink, Stderr: stderr},
			sink, m.Dir, "go", args...)
		if err != nil {
			return fmt.Errorf("[%s] go test -bench: %w", m.Dir, err)
		}
	}
	return nil
}

// hasBenchmarks reports whether captured `go test -bench` output
// contains at least one benchmark line. The token `Benchmark` at
// the start of a non-empty line is the canonical marker.
func hasBenchmarks(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Benchmark") {
			return true
		}
	}
	return false
}
