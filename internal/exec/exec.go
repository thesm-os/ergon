// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package exec is the shared subprocess seam every ergon subsystem
// uses to shell out. Production code passes [Command], which wraps
// os/exec; tests pass an in-memory fake so they can assert on
// invocations without touching the host environment.
//
// The interface is small on purpose. Run executes a process with a
// fixed working directory and an output destination; LookPath asks
// whether a binary is on PATH. Anything more elaborate (parallel
// fanout, prefixed multi-module output) layers on top.
package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Options bundles the per-call subprocess settings.
type Options struct {
	// Dir is the working directory the subprocess runs in. An empty
	// value means "inherit the caller's CWD"; ergon callers always
	// pass a resolved absolute path.
	Dir string

	// Stdout receives the subprocess's standard output. A nil value
	// discards.
	Stdout io.Writer

	// Stderr receives the subprocess's standard error. A nil value
	// discards.
	Stderr io.Writer
}

// Runner shells out to subprocesses and probes PATH. Implementations
// are [Command] in production and test-only fakes elsewhere.
type Runner interface {
	// Run executes name with args under opts. Returns the subprocess
	// exit error verbatim — callers wrap it with the contextual
	// information they have (which module, which step).
	Run(ctx context.Context, opts Options, name string, args ...string) error

	// LookPath reports whether name resolves to an executable on
	// PATH. The return shape mirrors os/exec.LookPath: a resolved
	// path on success, an error wrapping ErrNotFound when missing.
	LookPath(name string) (string, error)
}

// Command is the production Runner. It wraps os/exec one-to-one
// and adds nothing else; the wrapping is purely to give tests a
// seam they can stub.
type Command struct{}

// Run shells out to name with args, applying opts via the
// underlying os/exec.Command.
func (Command) Run(ctx context.Context, opts Options, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = opts.Dir
	c.Stdout = opts.Stdout
	c.Stderr = opts.Stderr
	return c.Run()
}

// LookPath wraps os/exec.LookPath unchanged.
func (Command) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// noPackagesSignals are the stderr fragments tools emit when
// `./...` matches no packages under the current build tags. The
// tokens are stable across recent versions of each tool and
// identify a benign "nothing to do for this module" condition
// rather than a real failure.
//
//   - `matched no packages` — the Go toolchain (vet, test, build,
//     generate) and anything that internally calls `go list`.
//   - `no go files to analyze` — golangci-lint v2.
//   - `no Go files in` — alternative phrasing emitted by some
//     tools (e.g. older govulncheck variants).
var noPackagesSignals = [][]byte{
	[]byte("matched no packages"),
	[]byte("no go files to analyze"),
	[]byte("no Go files in"),
}

// RunAllowNoPackages runs name with args via runner and treats any
// of the well-known "matched no packages" stderr signals as a soft
// skip rather than a hard error. Used by every multi-module
// command that walks `./...` per module so a module whose packages
// are all gated by build tags (e.g. an integration-tests module
// behind `//go:build integration`) does not fail the run.
//
// label is the per-module prefix the notice is tagged with so
// users can see which module was skipped. opts.Stderr still
// receives the subprocess's real stderr in every path — the skip
// path also prints a clean one-line summary on notice so users
// have an unambiguous signal that the failure was tolerated.
func RunAllowNoPackages(
	ctx context.Context, runner Runner, opts Options,
	notice io.Writer, label, name string, args ...string,
) error {
	var captured bytes.Buffer
	teedOpts := opts
	if opts.Stderr == nil {
		teedOpts.Stderr = &captured
	} else {
		teedOpts.Stderr = io.MultiWriter(opts.Stderr, &captured)
	}
	err := runner.Run(ctx, teedOpts, name, args...)
	if err != nil && matchesNoPackages(captured.Bytes()) {
		fmt.Fprintf(notice,
			"[%s] no packages match the current build tags; skipped\n", label)
		return nil
	}
	return err
}

// matchesNoPackages reports whether stderr carries any of the
// recognised "no packages" signals.
func matchesNoPackages(stderr []byte) bool {
	for _, sig := range noPackagesSignals {
		if bytes.Contains(stderr, sig) {
			return true
		}
	}
	return false
}
