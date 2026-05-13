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
	"context"
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
