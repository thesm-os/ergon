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

	"go.thesmos.sh/ergon/internal/checks/branch"
	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	"go.thesmos.sh/ergon/internal/checks/mutation"
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

// checkCmd is `ergon check`. Bare invocation runs the umbrella
// pre-merge gate: mod verify → lint → test (which produces the
// coverage profiles) → coverage thresholds → skip-expiry →
// error-prefix → vuln, plus the two slow gates when the user has
// opted in via `.ergon.yaml`:
//
//   - mutation: runs when `checks.mutation.packages` is non-empty
//     (declaring per-layer score / coverage thresholds).
//   - branch:   runs when any layer under `checks.coverage.packages`
//     sets `require_branch: true`.
//
// Both gates are minutes-slow per layer (gremlins re-runs the
// test suite against mutated source; gobco rebuilds each package
// under test), so the umbrella stays fast when neither is
// declared. Subcommands (`ergon check mutation`, `ergon check
// branch`) are always available regardless of configuration.
//
// Aggregation: by default every stage runs even if an earlier one
// failed, and a closing summary block lists each stage's verdict
// so the user sees every problem at once. Pass `--fast` / `-f` to
// short-circuit at the first stage failure (the dev-loop
// ergonomic).
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run the full pre-merge gate",
	Long: "Runs the umbrella check sequence: mod verify, lint, test, " +
		"coverage, skip-expiry, error-prefix, and vuln. The mutation " +
		"and branch gates are appended automatically when their " +
		"respective `.ergon.yaml` thresholds are declared (mutation: " +
		"`checks.mutation.packages` non-empty; branch: any coverage " +
		"layer with `require_branch: true`).\n\nBy default every stage " +
		"runs and the closing summary lists each stage's verdict; pass " +
		"--fast (-f) to abort at the first failure. Subcommands run " +
		"individual stages.",
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

		// The mutation and branch gates are opt-in per configuration:
		// they run only when the user has declared per-layer
		// thresholds (mutation.packages non-empty; coverage layer
		// with require_branch: true). Both are slow — gremlins
		// re-runs the test suite against mutated source, gobco
		// rebuilds each package under test — so the umbrella stays
		// fast when neither is configured.
		if len(cfg.Checks.Mutation.Packages) > 0 {
			stages = append(stages, checkStage{
				name: "mutation",
				run: func() error {
					return mutation.Run(ctx, runner, stdout, stderr,
						root, cfg.Checks.Mutation,
						cfg.Checks.Excludes, cfg.Checks.Skips,
						mutation.RunOptions{})
				},
			})
		}
		if anyRequiresBranch(cfg.Checks.Coverage.Packages) {
			stages = append(stages, checkStage{
				name: "branch",
				run: func() error {
					return branch.Run(ctx, runner, stdout, stderr,
						root, imports, cfg.Checks.Coverage,
						cfg.Checks.Excludes, cfg.Checks.Skips,
						branch.RunOptions{})
				},
			})
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

// anyRequiresBranch reports whether at least one declared layer
// in `.ergon.yaml` sets `require_branch: true`. The umbrella uses
// this signal to opt-in the gobco-backed branch gate: a workspace
// without any required layer skips the (slow) gobco pass
// entirely, while a single `require_branch: true` flips it on.
func anyRequiresBranch(layers []coverage.Layer) bool {
	for _, l := range layers {
		if l.RequireBranch {
			return true
		}
	}
	return false
}

// init attaches checkCmd to the root. Subcommand files attach
// themselves to checkCmd from their own init() functions.
func init() {
	rootCmd.AddCommand(checkCmd)
}
