// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package test

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
	"go.thesmos.sh/ergon/internal/stage"
)

// ErrCoverageDirUnset reports that the cobra layer invoked
// [Coverage] without configuring an output directory.
var ErrCoverageDirUnset = errors.New("test: coverage dir not set")

// Coverage converts every per-module `.out` profile under
// in.CoverageDir into a sibling HTML report via `go tool cover
// -html`. The styled per-module summary surfaces the total
// percentage `go tool cover -func` reports plus the sibling HTML
// path, so the reader sees the salient result without opening the
// file.
//
// Modules whose profile does not exist (e.g., a module with no
// tests) render as a dimmed skip; the rest of the run proceeds.
//
// Requires `ergon test` to have run first to produce the profiles;
// this command does not run tests itself.
func Coverage(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, opts stage.Options,
) error {
	if in.CoverageDir == "" {
		return ErrCoverageDirUnset
	}
	_ = stderr // captured per-call below; the stage renderer surfaces it.
	return stage.PerModule(ctx, stdout, in.Modules, opts,
		"go tool cover", "render per-module HTML reports + total %",
		func(ctx context.Context, m modules.Module) stage.StepResult {
			return coverageOne(ctx, runner, in.Root, in.CoverageDir, m)
		})
}

// coverageOne is one module's slice of [Coverage]: stat the
// profile, parse the total percent via `go tool cover -func`, then
// emit the HTML via `go tool cover -html`. The returned
// [stage.StepResult] carries the percent + HTML path as a success-
// path note so the rendered verdict line surfaces both inline.
func coverageOne(
	ctx context.Context, runner xexec.Runner,
	root, coverageDir string, m modules.Module,
) stage.StepResult {
	profile := coverageFile(coverageDir, m)
	if _, err := os.Stat(profile); err != nil {
		return stage.StepResult{Skipped: true}
	}

	var funcOut bytes.Buffer
	if err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &funcOut, Stderr: &funcOut},
		"go", "tool", "cover", "-func="+profile,
	); err != nil {
		return stage.StepResult{Err: err, Output: funcOut.String()}
	}

	html := strings.TrimSuffix(profile, ".out") + ".html"
	var htmlErr bytes.Buffer
	if err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: io.Discard, Stderr: &htmlErr},
		"go", "tool", "cover", "-html="+profile, "-o", html,
	); err != nil {
		return stage.StepResult{Err: err, Output: htmlErr.String()}
	}

	pct, ok := parseTotalPercent(funcOut.String())
	relProfile := relativePath(root, profile)
	relHTML := relativePath(root, html)
	note := relProfile + " → " + relHTML
	if ok {
		note = fmt.Sprintf("%5.1f%%  %s → %s", pct, relProfile, relHTML)
	}
	return stage.StepResult{Note: note}
}

// relativePath returns p relative to root in forward-slash form,
// or p verbatim when the relative computation fails (e.g. the
// paths live on different volumes on Windows). Centralised so the
// rendered note stays portable across operating systems.
func relativePath(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

// parseTotalPercent extracts the percent value from the `total:`
// row of `go tool cover -func` output. Returns ok=false when the
// row is absent or malformed.
func parseTotalPercent(out string) (float64, bool) {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "total:" {
			continue
		}
		raw := strings.TrimSuffix(fields[len(fields)-1], "%")
		pct, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, false
		}
		return pct, true
	}
	return 0, false
}

// readFile is a thin wrapper around os.ReadFile that returns the
// file's contents as a string. Centralised so [scanFuzzInFile] can
// be swapped behind a seam in tests if the need arises.
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
