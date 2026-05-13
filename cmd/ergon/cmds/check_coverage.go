// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/coverage"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// checkCoverageFlags groups the local flags `ergon check coverage`
// exposes. --ranges toggles the per-target uncovered-block dump;
// --no-test skips the integrated test run that produces fresh
// profiles, falling back to whatever `.out` files happen to exist.
var checkCoverageFlags struct {
	ranges bool
	noTest bool
}

// checkCoverageCmd is `ergon check coverage`. Runs the test suite
// (producing fresh per-module `.out` profiles), merges them,
// invokes `go tool cover -func`, and fails any function below its
// layer's minimum coverage.
//
// Positional arguments restrict the run to a subset of the
// configured layers — each arg is matched as a layer prefix
// (e.g. `core/kernel/fold`). With no args every layer in
// `checks.coverage.packages` runs.
var checkCoverageCmd = &cobra.Command{
	Use:   "coverage [target...]",
	Short: "Enforce per-layer coverage thresholds",
	Long: "Runs `ergon test` to produce fresh per-module `.out` profiles, " +
		"merges them, runs `go tool cover -func`, and fails any function " +
		"below its layer's minimum coverage as declared under " +
		"`.ergon.yaml`'s `checks.coverage` section.\n\n" +
		"Positional arguments restrict the run to specific layer prefixes; " +
		"with none, every configured layer is exercised. The --ranges flag " +
		"appends the uncovered block ranges for every failing target. The " +
		"--no-test flag skips the integrated test run and operates on " +
		"whatever profiles already exist (useful when iterating on " +
		"thresholds without paying for a re-run).",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
		runner := xexec.Command{}

		in, err := testInputs(ctx)
		if err != nil {
			return err
		}
		if !checkCoverageFlags.noTest {
			if err := test.Run(ctx, runner, stdout, stderr,
				in, cfg.Test, test.Override{}, stageOpts()); err != nil {
				return err
			}
		}
		return coverage.Run(ctx, runner, stdout, stderr,
			in.Root, in.CoverageDir, in.Imports, cfg.Checks.Coverage,
			cfg.Checks.Excludes, cfg.Checks.Skips,
			coverage.RunOptions{Targets: args, Verbose: checkCoverageFlags.ranges})
	},
}

func init() {
	checkCoverageCmd.Flags().BoolVar(&checkCoverageFlags.ranges, "ranges", false,
		"dump uncovered block ranges (file:start-end (stmts)) for every failing target")
	checkCoverageCmd.Flags().BoolVar(&checkCoverageFlags.noTest, "no-test", false,
		"skip the integrated `ergon test` run; operate on existing profiles")
	checkCmd.AddCommand(checkCoverageCmd)
}
