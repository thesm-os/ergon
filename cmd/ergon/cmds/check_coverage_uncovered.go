// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/coverage"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// checkCoverageUncoveredFlags captures the local flags on
// `check coverage uncovered`. --all flips the report from
// policy-filtered (the default) to "show every uncovered block";
// --no-test skips the integrated test run and operates on
// whatever `.out` files already exist.
var checkCoverageUncoveredFlags struct {
	all    bool
	noTest bool
}

// checkCoverageUncoveredCmd is `ergon check coverage uncovered`.
// Runs the test suite (producing fresh per-module `.out`
// profiles), then renders every uncovered block grouped by file →
// function. By default the report respects the same policy the
// gate command applies (layer membership + `checks.excludes` +
// `checks.skips`); `--all` removes the filters so the full
// coverage debt surfaces. `--no-test` skips the integrated test
// run.
var checkCoverageUncoveredCmd = &cobra.Command{
	Use:   "uncovered",
	Short: "List every uncovered line, grouped by file → function",
	Long: "Runs `ergon test` to produce fresh `.out` profiles, then maps " +
		"every count=0 block in the merged coverprofile to its " +
		"containing function and prints the result grouped by file. " +
		"By default the report respects the same policy the gate " +
		"command applies: only functions under " +
		"`checks.coverage.packages` layers, minus `checks.excludes` " +
		"and `checks.skips`. Pass --all to disable the filters and " +
		"show every uncovered block across every package the test " +
		"suite touched. Pass --no-test to operate on existing " +
		"profiles instead of running the suite again.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
		runner := xexec.Command{}

		in, err := testInputs(ctx)
		if err != nil {
			return err
		}
		if !checkCoverageUncoveredFlags.noTest {
			if err := test.Run(ctx, runner, stdout, stderr,
				in, cfg.Test, test.Override{}, stageOpts()); err != nil {
				return err
			}
		}
		return coverage.Uncovered(ctx, runner, stdout, stderr,
			in.Root, in.CoverageDir, in.Imports,
			cfg.Checks.Coverage, cfg.Checks.Excludes, cfg.Checks.Skips,
			coverage.UncoveredOptions{All: checkCoverageUncoveredFlags.all})
	},
}

func init() {
	checkCoverageUncoveredCmd.Flags().BoolVar(
		&checkCoverageUncoveredFlags.all, "all", false,
		"Skip the policy filters and show every uncovered block",
	)
	checkCoverageUncoveredCmd.Flags().BoolVar(
		&checkCoverageUncoveredFlags.noTest, "no-test", false,
		"skip the integrated `ergon test` run; operate on existing profiles",
	)
	checkCoverageCmd.AddCommand(checkCoverageUncoveredCmd)
}
