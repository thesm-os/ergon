// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/branch"
	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/checks/mutation"
	"go.thesmos.sh/ergon/internal/config"
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

// checkOnly / checkSkip capture the per-invocation CLI overrides
// for `ergon check`'s stage filter. They compose with the config
// values (`checks.enabled` / `checks.disabled` in `.ergon.yaml`)
// per the precedence rules on [stage.Filter].
var (
	checkOnly []string
	checkSkip []string
)

// checkCmd is `ergon check`. The umbrella runs the pre-merge gate
// composed entirely of test-derived stages and the orchestrators
// that feed them:
//
//   - mod      `go mod verify` (clean tidy precondition)
//   - lint     the full static-analysis suite (see `ergon lint`)
//   - test     `go test` per module + coverage profile collection
//   - coverage per-layer line-coverage thresholds
//   - mutation per-layer gremlins thresholds (opt-in)
//   - branch   per-layer gobco thresholds (opt-in)
//
// The static-analysis gates that used to live here
// (`skip-expiry`, `error-prefix`, `vuln`) moved into `ergon lint`
// where they belong by nature — none of them needs test artefacts
// — and the umbrella picks them up implicitly via its `lint`
// stage. The split lets `ergon lint` run as a fast pre-PR gate
// without paying the test-execution cost.
//
// Both mutation and branch are minutes-slow per layer; the
// umbrella appends them only when their respective `.ergon.yaml`
// thresholds are declared:
//
//   - mutation: runs when `checks.mutation.packages` is non-empty.
//   - branch:   runs when any `checks.coverage.packages` layer
//     sets `require_branch: true`.
//
// The stage list can be narrowed via `.ergon.yaml`'s
// `checks.enabled` / `checks.disabled` or per-invocation via
// `--only` / `--skip`. An unknown stage name surfaces as a usage
// error so typos are caught at start time.
//
// Aggregation: by default every (filtered-in) stage runs even if
// an earlier one failed, and a closing summary block lists each
// stage's verdict so the user sees every problem at once. Pass
// `--fast` / `-f` to short-circuit at the first stage failure
// (the dev-loop ergonomic).
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run the full pre-merge gate",
	Long: "Runs the umbrella pre-merge gate: mod verify, lint (the " +
		"full static-analysis suite — see `ergon lint`), test, and " +
		"coverage. The mutation and branch gates are appended " +
		"automatically when their `.ergon.yaml` thresholds are " +
		"declared (mutation: `checks.mutation.packages` non-empty; " +
		"branch: any coverage layer with `require_branch: true`).\n\n" +
		"The stage list can be narrowed via `.ergon.yaml`'s " +
		"`checks.enabled` / `checks.disabled` or per-invocation via " +
		"`--only` / `--skip`. Stage names are `mod`, `lint`, `test`, " +
		"`coverage`, plus the opt-in `mutation` and `branch`.\n\n" +
		"By default every stage runs and the closing summary lists " +
		"each stage's verdict; pass --fast (-f) to abort at the first " +
		"failure.",
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
		coverageDir := filepath.Join(root, config.ArtifactDir, "coverage")

		in, err := testInputs(ctx)
		if err != nil {
			return err
		}

		opts := stageOpts()
		// lintFilter forwards the lint-level Enabled/Disabled config
		// when the umbrella delegates to lint.All; CLI --only/--skip
		// on `ergon check` apply to the check stages, NOT to the
		// nested lint stages (those have their own flags on
		// `ergon lint`).
		lintFilter := stage.Filter{
			Enabled:  cfg.Lint.Enabled,
			Disabled: cfg.Lint.Disabled,
		}
		stages := []stage.Named{
			{Name: "mod", Run: func() error { return mod.Verify(ctx, runner, stdout, stderr, root, mods, opts) }},
			{Name: "lint", Run: func() error {
				return lint.All(ctx, runner, stdout, stderr,
					lint.Inputs{Root: root, Modules: mods, GitFiles: gitFilesFor(ctx, runner, root)},
					cfg.Markdown, cfg.License, cfg.Lint.ErrorPrefix,
					lintFilter, opts)
			}},
			{Name: "test", Run: func() error {
				return test.Run(ctx, runner, stdout, stderr, in, cfg.Test, test.Override{}, opts)
			}},
			{Name: "coverage", Run: func() error {
				return coverage.Run(ctx, runner, stdout, stderr,
					root, coverageDir, imports, cfg.Checks.Coverage,
					cfg.Checks.Excludes, cfg.Checks.Skips, coverage.RunOptions{})
			}},
		}

		// The mutation and branch gates are opt-in per configuration:
		// they run only when the user has declared per-layer
		// thresholds (mutation.packages non-empty; coverage layer
		// with require_branch: true). Both are slow — gremlins
		// re-runs the test suite against mutated source, gobco
		// rebuilds each package under test — so the umbrella stays
		// fast when neither is configured.
		if len(cfg.Checks.Mutation.Packages) > 0 {
			stages = append(stages, stage.Named{
				Name: "mutation",
				Run: func() error {
					return mutation.Run(ctx, runner, stdout, stderr,
						root, cfg.Checks.Mutation,
						cfg.Checks.Excludes, cfg.Checks.Skips,
						mutation.RunOptions{})
				},
			})
		}
		if anyRequiresBranch(cfg.Checks.Coverage.Packages) {
			stages = append(stages, stage.Named{
				Name: "branch",
				Run: func() error {
					return branch.Run(ctx, runner, stdout, stderr,
						root, imports, cfg.Checks.Coverage,
						cfg.Checks.Excludes, cfg.Checks.Skips,
						branch.RunOptions{})
				},
			})
		}

		// Apply the stage filter — config-side enabled/disabled
		// composed with the CLI --only/--skip overrides. An unknown
		// stage name surfaces here as a usage error before any work
		// runs, so typos are caught fast.
		selected, err := stage.Filter{
			Enabled:  cfg.Checks.Enabled,
			Disabled: cfg.Checks.Disabled,
			Only:     checkOnly,
			Skip:     checkSkip,
		}.Apply(stages)
		if err != nil {
			return err
		}

		s := style.Detect(stdout)
		results := make([]style.StageResult, 0, len(selected))
		var failures []error
		for _, st := range selected {
			if err := st.Run(); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", st.Name, err))
				results = append(results, style.StageResult{
					Label: st.Name, Err: err, Note: "see report above",
				})
				if fastMode {
					break
				}
				continue
			}
			results = append(results, style.StageResult{Label: st.Name})
		}

		s.Header(stdout, "check summary", "per-stage verdicts for this run")
		pass := "every stage passed"
		fail := fmt.Sprintf("%d of %d stage(s) failed (see per-stage reports above)",
			len(failures), len(selected))
		s.Summary(stdout, results, pass, fail)
		if len(failures) == 0 {
			return nil
		}
		return errors.Join(failures...)
	},
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

// init attaches checkCmd to the root and binds the stage-filter
// flags. Subcommand files attach themselves to checkCmd from their
// own init() functions.
func init() {
	checkCmd.Flags().StringSliceVar(&checkOnly, "only", nil,
		"run only these check stages (comma-separated; beats --skip and the config)")
	checkCmd.Flags().StringSliceVar(&checkSkip, "skip", nil,
		"skip these check stages (comma-separated; unions with checks.disabled in config)")
	rootCmd.AddCommand(checkCmd)
}
