// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package stage is the per-module fan-out helper every ergon gate
// subsystem (lint, build, test, vuln, mod) shares.
//
// One [PerModule] call renders the section header, iterates every
// discovered module — honouring the user's --fast preference —
// and writes the closing summary block via
// [go.thesmos.sh/ergon/internal/style.Style.Summary]. The
// subsystem callback returns (skipped, err); skipped=true marks
// a build-tag-gated soft skip that the report records as a dimmed
// dash rather than a pass.
//
// Aggregation: when fast is false (the CI default) every module
// runs and errors are joined via [errors.Join] so the caller sees
// every failure at once; when fast is true the iteration aborts
// at the first failure for the dev loop.
package stage

import (
	"context"
	"errors"
	"fmt"
	"io"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/style"
)

// PerModule runs fn against every module and renders the section
// header + summary block around the calls. Returns nil when every
// module passed; otherwise an [errors.Join] of every failure.
//
// fn receives the module to act on and returns (skipped, err):
//   - skipped=true means the work was intentionally not performed
//     (e.g. a module whose packages are all gated by build tags);
//     the summary shows a dimmed dash, no failure is recorded.
//   - err!=nil records a failure; in fast mode iteration aborts.
//   - both zero is the happy path.
//
// title and details feed [style.Style.Header]; passMessage and
// failMessage feed [style.Style.Summary] (the latter accepts an
// empty value, in which case the renderer composes a default
// "N of M target(s) failed" line).
func PerModule(
	ctx context.Context, stdout io.Writer,
	mods []modules.Module, fast bool, title, details string,
	fn func(context.Context, modules.Module) (bool, error),
) error {
	s := style.Detect(stdout)
	s.Header(stdout, title, details)
	results := make([]style.StageResult, 0, len(mods))
	var failures []error
	for _, m := range mods {
		skipped, err := fn(ctx, m)
		results = append(results, makeResult(m.Dir, skipped, err))
		if err != nil {
			failures = append(failures, fmt.Errorf("[%s]: %w", m.Dir, err))
			if fast {
				break
			}
		}
	}
	pass := fmt.Sprintf("every module passed %s", title)
	var fail string
	if len(failures) > 0 {
		fail = fmt.Sprintf("%d of %d module(s) failed %s", len(failures), len(mods), title)
	}
	s.Summary(stdout, results, pass, fail)
	if len(failures) == 0 {
		return nil
	}
	return errors.Join(failures...)
}

// RunAllowSkip wraps [xexec.RunAllowNoPackages] so the caller
// learns whether the command was soft-skipped (build tags
// excluded every package) vs ran to completion. Returns
// (skipped, err).
//
// The skip notice from [xexec.RunAllowNoPackages] still streams
// through to notice unchanged so users see the inline message in
// the section body; skipped=true reflects that the notice fired.
func RunAllowSkip(
	ctx context.Context, runner xexec.Runner, opts xexec.Options,
	notice io.Writer, label, name string, args ...string,
) (bool, error) {
	probe := &skipDetector{inner: notice}
	if err := xexec.RunAllowNoPackages(ctx, runner, opts, probe, label, name, args...); err != nil {
		return false, err
	}
	return probe.skipped, nil
}

// skipDetector is an [io.Writer] wrapper that flags itself when
// [xexec.RunAllowNoPackages] writes its skip notice through. The
// wrapped writer still receives the bytes verbatim.
type skipDetector struct {
	inner   io.Writer
	skipped bool
}

func (s *skipDetector) Write(p []byte) (int, error) {
	s.skipped = true
	return s.inner.Write(p)
}

// makeResult builds the [style.StageResult] for one per-module
// outcome. skipped=true is recorded as a dimmed
// "skipped (no packages match build tags)"; a non-nil err carries
// a one-line summary; the happy path leaves Note empty.
func makeResult(label string, skipped bool, err error) style.StageResult {
	switch {
	case skipped:
		return style.StageResult{
			Label: label, Skipped: true,
			Note: "skipped (no packages match build tags)",
		}
	case err != nil:
		return style.StageResult{Label: label, Err: err, Note: err.Error()}
	default:
		return style.StageResult{Label: label}
	}
}
