// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package stage is the per-module fan-out helper every ergon gate
// subsystem (lint, build, test, vuln, mod) shares.
//
// One [PerModule] call renders the section header, iterates every
// discovered module — honouring the user's --fast preference —
// and writes the closing summary block via
// [go.thesmos.sh/ergon/internal/style.Style.Summary]. The
// subsystem callback returns a [StepResult]; on failure its
// captured output is rendered indented + dimmed beneath the
// failing verdict line, on success the output is dropped, on a
// build-tag soft skip a dimmed dash replaces the verdict.
//
// Aggregation: when fast is false (the CI default) every module
// runs and errors are joined via [errors.Join] so the caller sees
// every failure at once; when fast is true the iteration aborts
// at the first failure for the dev loop.
//
// Verbosity: when verbose is false (the default) tool stdout and
// stderr are captured into a buffer and shown only on failure;
// when true the raw output streams live so the user can watch
// long-running operations.
package stage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/style"
)

// Options bundles the per-call modes a gate subsystem inherits
// from the root cobra flags.
type Options struct {
	// Fast aborts the per-module iteration at the first failure.
	// False (the CI default) runs every module and aggregates
	// failures via [errors.Join].
	Fast bool

	// Verbose streams the underlying tool's stdout and stderr
	// live to the caller's writers. False (the default) captures
	// both into a buffer and reveals the buffer only when the
	// command fails (indented + dimmed beneath the verdict line).
	Verbose bool
}

// StepResult is what a [PerModule] callback returns: the outcome
// classification plus any captured tool output the renderer should
// surface on failure.
type StepResult struct {
	// Skipped is true when the work was intentionally not
	// performed (e.g. a module whose packages are all gated by
	// build tags). The renderer shows a dimmed dash in place of
	// the verdict; no failure is recorded.
	Skipped bool

	// Output is the captured stdout+stderr from the underlying
	// tool. Empty in verbose mode (output streamed live) or on a
	// clean pass. When Err is non-nil and Output is non-empty the
	// summary block indents + dims Output beneath the failing
	// verdict line.
	Output string

	// Err is the subprocess error, or nil on pass.
	Err error
}

// PerModule runs fn against every module and renders the section
// header + summary block around the calls. Returns nil when every
// module passed; otherwise an [errors.Join] of every failure.
//
// title and details feed [style.Style.Header]; the closing
// summary message is composed from a count of failures.
func PerModule(
	ctx context.Context, stdout io.Writer,
	mods []modules.Module, opts Options, title, details string,
	fn func(context.Context, modules.Module) StepResult,
) error {
	s := style.Detect(stdout)
	s.Header(stdout, title, details)
	results := make([]style.StageResult, 0, len(mods))
	var failures []error
	for _, m := range mods {
		r := fn(ctx, m)
		results = append(results, makeResult(m.Dir, r))
		if r.Err != nil {
			failures = append(failures, fmt.Errorf("[%s]: %w", m.Dir, r.Err))
			if opts.Fast {
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

// RunAllowSkip executes name with args via runner and returns a
// [StepResult] classifying the outcome. The behaviour depends on
// opts.Verbose:
//
//   - opts.Verbose=false (default): stdout and stderr are captured
//     into a single buffer. On success the buffer is dropped; on
//     failure it is returned in [StepResult.Output] for the
//     renderer to indent under the verdict. A "no packages"
//     stderr signal (see [xexec.IsNoPackagesSignal]) demotes the
//     failure to a soft skip — a one-line notice is printed to
//     notice and Skipped is set.
//
//   - opts.Verbose=true: stdout/stderr stream live to streamOut /
//     streamErr; the function additionally tees stderr into a
//     small buffer so the no-packages signal can still demote the
//     failure to a skip. [StepResult.Output] is left empty
//     because the bytes already reached the user.
//
// dir is the subprocess working directory; label is the
// per-module prefix the skip notice carries.
func RunAllowSkip(
	ctx context.Context, runner xexec.Runner,
	opts Options,
	dir, label string,
	streamOut, streamErr, notice io.Writer,
	name string, args ...string,
) StepResult {
	if opts.Verbose {
		return runStream(ctx, runner, dir, label, streamOut, streamErr, notice, name, args...)
	}
	return runBuffered(ctx, runner, dir, label, notice, name, args...)
}

// runStream is the verbose path: live output, stderr tee'd so the
// skip signal can still be detected.
func runStream(
	ctx context.Context, runner xexec.Runner,
	dir, label string,
	streamOut, streamErr, notice io.Writer,
	name string, args ...string,
) StepResult {
	var stderrBuf bytes.Buffer
	var stderrDst io.Writer = &stderrBuf
	if streamErr != nil {
		stderrDst = io.MultiWriter(streamErr, &stderrBuf)
	}
	err := runner.Run(ctx,
		xexec.Options{Dir: dir, Stdout: streamOut, Stderr: stderrDst},
		name, args...)
	if err != nil && xexec.IsNoPackagesSignal(stderrBuf.Bytes()) {
		fmt.Fprintf(notice,
			"[%s] no packages match the current build tags; skipped\n", label)
		return StepResult{Skipped: true}
	}
	return StepResult{Err: err}
}

// runBuffered is the default path: combined output captured to one
// buffer; on pass the buffer is dropped, on failure it is returned
// for the renderer to indent under the verdict line.
func runBuffered(
	ctx context.Context, runner xexec.Runner,
	dir, label string,
	notice io.Writer,
	name string, args ...string,
) StepResult {
	var combined bytes.Buffer
	err := runner.Run(ctx,
		xexec.Options{Dir: dir, Stdout: &combined, Stderr: &combined},
		name, args...)
	if err != nil && xexec.IsNoPackagesSignal(combined.Bytes()) {
		fmt.Fprintf(notice,
			"[%s] no packages match the current build tags; skipped\n", label)
		return StepResult{Skipped: true}
	}
	if err != nil {
		return StepResult{Err: err, Output: combined.String()}
	}
	return StepResult{}
}

// makeResult builds the [style.StageResult] for one per-module
// outcome. Skipped maps to a dimmed dash; a non-nil err carries
// the error message as Note and the captured tool output (when
// present) as Output; the happy path leaves both blank.
func makeResult(label string, r StepResult) style.StageResult {
	switch {
	case r.Skipped:
		return style.StageResult{
			Label: label, Skipped: true,
			Note: "skipped (no packages match build tags)",
		}
	case r.Err != nil:
		return style.StageResult{
			Label:  label,
			Err:    r.Err,
			Note:   r.Err.Error(),
			Output: r.Output,
		}
	default:
		return style.StageResult{Label: label}
	}
}
