// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package markdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// missingHint is emitted on stderr when markdownlint-cli2 is not on
// PATH. The hint points at `ergon bootstrap` so users have a clear
// remediation path.
const missingHint = "markdownlint-cli2 not on PATH; " +
	"skipping markdown step (run `ergon bootstrap` to install)"

// Format runs markdownlint-cli2 with `--fix`, rewriting files in
// place where the linter can auto-fix issues. Used by `ergon fmt`.
// Failures during the run are surfaced as a warning on stderr, not
// returned as errors — `ergon lint` is the read-only gate.
func Format(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, cfg Config,
) error {
	if len(cfg.Globs) == 0 {
		return nil
	}
	present, err := available(runner, stderr)
	if err != nil || !present {
		return err
	}
	fmt.Fprintln(stdout, "[.] markdownlint --fix")
	args := slices.Concat([]string{"--fix"}, cfg.Globs)
	if err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: stdout, Stderr: stderr},
		"markdownlint-cli2", args...); err != nil {
		fmt.Fprintln(stderr, "warning: markdownlint:", err)
	}
	return nil
}

// Lint runs markdownlint-cli2 in reporting mode (no --fix) and
// returns a non-nil error when any Markdown file fails the rules
// declared in `.markdownlint.yml`. Used by `ergon lint`.
func Lint(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, cfg Config,
) error {
	if len(cfg.Globs) == 0 {
		return nil
	}
	present, err := available(runner, stderr)
	if err != nil || !present {
		return err
	}
	fmt.Fprintln(stdout, "[.] markdownlint")
	return runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: stdout, Stderr: stderr},
		"markdownlint-cli2", cfg.Globs...)
}

// available reports whether markdownlint-cli2 resolves on PATH.
// Returns (false, nil) when the binary is genuinely missing — the
// caller treats that as a best-effort skip and emits a warning.
// Returns (false, err) for other lookup failures (permission etc.)
// so the caller propagates the unexpected condition.
func available(runner xexec.Runner, stderr io.Writer) (bool, error) {
	_, err := runner.LookPath("markdownlint-cli2")
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, exec.ErrNotFound):
		fmt.Fprintln(stderr, "warning:", missingHint)
		return false, nil
	default:
		return false, fmt.Errorf("lookup markdownlint-cli2: %w", err)
	}
}
