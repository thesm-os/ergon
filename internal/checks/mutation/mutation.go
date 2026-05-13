// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package mutation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// Run is `ergon check mutation`: iterates every layer in
// cfg.Packages, runs `gremlins unleash` against the layer's
// directory, parses gremlins' output for `Test efficacy` and
// `Mutator coverage` percentages, and fails any layer below
// either threshold.
//
// An empty cfg.Packages short-circuits with a notice — the
// project simply has no thresholds declared yet.
//
// gremlins' own exit code is unreliable (the tool has been
// observed to exit 0 regardless of measured efficacy in some
// versions); ergon parses the percentages out of stdout and
// enforces both thresholds itself.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, cfg Config,
) error {
	if len(cfg.Packages) == 0 {
		fmt.Fprintln(stdout, "mutation: no thresholds declared in .ergon.yaml; skipping")
		return nil
	}
	cfg = withDefaults(cfg)

	var failures []string
	for _, layer := range cfg.Packages {
		dir := layerDir(layer.Path)
		full := filepath.Join(root, dir)
		if info, err := os.Stat(full); err != nil || !info.IsDir() {
			fmt.Fprintf(stdout, "[%s] skip — directory missing\n", dir)
			continue
		}

		fmt.Fprintf(stdout, "[%s] gremlins unleash (workers=%d, test-cpu=%d, timeout=%d)\n",
			dir, cfg.Gremlins.Workers, cfg.Gremlins.TestCPU, cfg.Gremlins.TimeoutCoefficient)
		out, runErr := runGremlins(ctx, runner, full, cfg.Gremlins)
		score, coverage, parseErr := parseGremlinsOutput(out)
		if parseErr != nil {
			if runErr != nil {
				return fmt.Errorf("[%s] gremlins: %w: %s", dir, runErr, strings.TrimSpace(out))
			}
			return fmt.Errorf("[%s] %w", dir, parseErr)
		}

		coverageThreshold := layer.Coverage
		if coverageThreshold == 0 {
			coverageThreshold = layer.Score
		}
		passScore := score >= layer.Score
		passCov := coverage >= coverageThreshold

		fmt.Fprintf(stdout, "[%s] score=%d%% (≥%d%% %s) coverage=%d%% (≥%d%% %s)\n",
			dir, score, layer.Score, verdict(passScore),
			coverage, coverageThreshold, verdict(passCov))

		if !passScore || !passCov {
			failures = append(failures, fmt.Sprintf("%s (score=%d/%d, coverage=%d/%d)",
				dir, score, layer.Score, coverage, coverageThreshold))
		}
	}

	if len(failures) > 0 {
		fmt.Fprintln(stderr, "mutation: layer(s) below threshold:")
		for _, f := range failures {
			fmt.Fprintln(stderr, "  "+f)
		}
		return fmt.Errorf("%d mutation layer(s) below threshold", len(failures))
	}
	return nil
}

// withDefaults fills any zero-value gremlins field on cfg from
// [Defaults]. Per-layer thresholds are not defaulted; the project
// declares them explicitly.
func withDefaults(cfg Config) Config {
	d := Defaults()
	if cfg.Gremlins.Workers == 0 {
		cfg.Gremlins.Workers = d.Gremlins.Workers
	}
	if cfg.Gremlins.TestCPU == 0 {
		cfg.Gremlins.TestCPU = d.Gremlins.TestCPU
	}
	if cfg.Gremlins.TimeoutCoefficient == 0 {
		cfg.Gremlins.TimeoutCoefficient = d.Gremlins.TimeoutCoefficient
	}
	if cfg.Gremlins.ExcludeFiles == "" {
		cfg.Gremlins.ExcludeFiles = d.Gremlins.ExcludeFiles
	}
	return cfg
}

// runGremlins invokes `gremlins unleash` against dir and returns
// the captured stdout+stderr. The subprocess exit code is
// propagated so the caller can distinguish "run failed to start"
// from "ran but produced low metrics".
func runGremlins(
	ctx context.Context, runner xexec.Runner, dir string, cfg GremlinsConfig,
) (string, error) {
	var buf bytes.Buffer
	args := []string{
		"unleash",
		"--exclude-files", cfg.ExcludeFiles,
		"--timeout-coefficient", strconv.Itoa(cfg.TimeoutCoefficient),
		"--workers", strconv.Itoa(cfg.Workers),
		"--test-cpu", strconv.Itoa(cfg.TestCPU),
		".",
	}
	err := runner.Run(ctx,
		xexec.Options{Dir: dir, Stdout: &buf, Stderr: &buf},
		"gremlins", args...)
	return buf.String(), err
}

// parseGremlinsOutput extracts the `Test efficacy:` and
// `Mutator coverage:` percentages from gremlins' stdout. Returns
// an error when neither line is present — a sign the tool failed
// to produce any metrics (zero covered mutants, crash, etc.).
func parseGremlinsOutput(out string) (score, coverage int, err error) {
	score = parsePercent(out, "Test efficacy:")
	coverage = parsePercent(out, "Mutator coverage:")
	if score == 0 && coverage == 0 && !strings.Contains(out, "Test efficacy:") {
		return 0, 0, fmt.Errorf("gremlins produced no metrics (zero covered mutants?)")
	}
	return score, coverage, nil
}

// parsePercent scans out for a line beginning with prefix and
// returns the integer percentage that follows. The bash script
// uses the same shape: `Test efficacy: 87.5%` → 87.
func parsePercent(out, prefix string) int {
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		rest = strings.TrimSuffix(rest, "%")
		f, parseErr := strconv.ParseFloat(rest, 64)
		if parseErr != nil {
			continue
		}
		return int(f)
	}
	return 0
}

// layerDir converts a layer glob like `foo/...` to the directory
// `foo` ergon hands to gremlins. Only the `<dir>/...` shape is
// supported today; arbitrary globs would require a recursive walk
// that gremlins itself does not.
func layerDir(glob string) string {
	return strings.TrimSuffix(glob, "/...")
}

// verdict returns the human-facing pass/fail tag rendered in the
// per-layer output line.
func verdict(pass bool) string {
	if pass {
		return "✓"
	}
	return "✗"
}
