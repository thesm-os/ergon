// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	"go.thesmos.sh/ergon/internal/checks/skipexpiry"
	"go.thesmos.sh/ergon/internal/checks/vuln"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/lint"
	"go.thesmos.sh/ergon/internal/mod"
	"go.thesmos.sh/ergon/internal/stage"
	"go.thesmos.sh/ergon/internal/style"
	"go.thesmos.sh/ergon/internal/test"
)

// stageOpts wraps the current root flags into the value
// [stage.PerModule] and the gate subsystems expect.
func stageOpts() stage.Options {
	return stage.Options{Fast: fastMode, Verbose: verboseMode}
}

// checkCmd is `ergon check`. Bare invocation runs the full
// pre-merge gate: mod verify → lint → test (which produces the
// coverage profiles) → coverage thresholds → skip-expiry →
// error-prefix → vuln.
//
// Aggregation: by default every stage runs even if an earlier one
// failed, and a closing summary block lists each stage's verdict
// so the user sees every problem at once. Pass `--fast` / `-f` to
// short-circuit at the first stage failure (the dev-loop
// ergonomic).
//
// Mutation testing (`ergon check mutation`) is intentionally NOT
// part of the umbrella. `gremlins unleash` runs minutes per layer
// and is not suitable for a pre-merge gate; run it explicitly via
// the subcommand on a nightly cadence or before a release.
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run the full pre-merge gate",
	Long: "Runs the umbrella check sequence: mod verify, lint, test, " +
		"coverage, skip-expiry, error-prefix, and vuln. By default every " +
		"stage runs and the closing summary lists each stage's verdict; " +
		"pass --fast (-f) to abort at the first failure. Subcommands run " +
		"individual stages.\n\nMutation testing is excluded — it is slow " +
		"and belongs in a nightly job. Run `ergon check mutation` explicitly.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
		runner := xexec.Command{}

		root, mods, err := discover.Resolve(ctx, cfg.Modules)
		if err != nil {
			return err
		}
		imports, err := discover.ModuleImports(root, mods)
		if err != nil {
			return err
		}
		name := cfg.Name
		if name == "" {
			name = filepath.Base(root)
		}
		coverageDir := filepath.Join(root, "."+name, "coverage")

		// goFiles is needed by skip-expiry and error-prefix. It is
		// resolved lazily so a failure here is itself recorded as
		// a stage outcome rather than a hard abort.
		var goFiles []string
		gitFiles := func() error {
			if goFiles != nil {
				return nil
			}
			files, gerr := discover.GitFiles(ctx, runner, root, ".go")
			if gerr != nil {
				return gerr
			}
			goFiles = files
			return nil
		}

		in, err := testInputs(ctx)
		if err != nil {
			return err
		}

		opts := stageOpts()
		stages := []checkStage{
			{"mod", func() error { return mod.Verify(ctx, runner, stdout, stderr, root, mods, opts) }},
			{"lint", func() error {
				return lint.All(ctx, runner, stdout, stderr,
					lint.Inputs{Root: root, Modules: mods}, cfg.Markdown, cfg.License, opts)
			}},
			{"test", func() error {
				return test.Run(ctx, runner, stdout, stderr, in, cfg.Test, test.Override{}, opts)
			}},
			{"coverage", func() error {
				return coverage.Run(ctx, runner, stdout, stderr,
					root, coverageDir, imports, cfg.Checks.Coverage,
					cfg.Checks.Excludes, cfg.Checks.Skips, coverage.RunOptions{})
			}},
			{"skip-expiry", func() error {
				if err := gitFiles(); err != nil {
					return err
				}
				return stage.Single(ctx, stdout, opts,
					"skip-expiry", "scan t.Skip() for an expiry date",
					"every t.Skip carries a parseable expiry date", "",
					func(_ context.Context, sOut, sErr io.Writer) error {
						return skipexpiry.Run(sOut, sErr, root, goFiles)
					})
			}},
			{"error-prefix", func() error {
				if err := gitFiles(); err != nil {
					return err
				}
				return stage.Single(ctx, stdout, opts,
					"error-prefix", "errors.New text starts with the package name",
					"every errors.New carries the expected package prefix", "",
					func(_ context.Context, sOut, sErr io.Writer) error {
						return errorprefix.Run(sOut, sErr, root, goFiles, cfg.Checks.ErrorPrefix)
					})
			}},
			{"vuln", func() error { return vuln.Run(ctx, runner, stdout, stderr, root, mods, opts) }},
		}

		s := style.Detect(stdout)
		results := make([]style.StageResult, 0, len(stages))
		var failures []error
		for _, st := range stages {
			if err := st.run(); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", st.name, err))
				results = append(results, style.StageResult{
					Label: st.name, Err: err, Note: "see report above",
				})
				if fastMode {
					break
				}
				continue
			}
			results = append(results, style.StageResult{Label: st.name})
		}

		s.Header(stdout, "check summary", "per-stage verdicts for this run")
		pass := "every stage passed"
		fail := fmt.Sprintf("%d of %d stage(s) failed (see per-stage reports above)",
			len(failures), len(stages))
		s.Summary(stdout, results, pass, fail)
		if len(failures) == 0 {
			return nil
		}
		return errors.Join(failures...)
	},
}

// checkStage names one umbrella stage and the function that runs
// it. The function's error is consumed by the aggregator —
// nothing else.
type checkStage struct {
	name string
	run  func() error
}

// init attaches checkCmd to the root. Subcommand files attach
// themselves to checkCmd from their own init() functions.
func init() {
	rootCmd.AddCommand(checkCmd)
}
