// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/bench"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// benchRegressionCmd is `ergon bench regression`. Runs the bench
// suite into a temp file and invokes `benchstat` against the
// pinned baseline.
var benchRegressionCmd = &cobra.Command{
	Use:   "regression",
	Short: "Diff a fresh benchmark sample against the baseline",
	Long: "Runs the same bench suite as `bench baseline`, then runs " +
		"`benchstat` to compare the fresh sample against the pinned " +
		"baseline file. Fails when the baseline file does not exist.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return bench.Regression(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, mods, cfg.Test, cfg.Bench)
	},
}

func init() {
	benchCmd.AddCommand(benchRegressionCmd)
}
