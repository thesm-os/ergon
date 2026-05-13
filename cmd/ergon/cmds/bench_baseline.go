// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/bench"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// benchBaselineCmd is `ergon bench baseline`. Runs the bench
// suite per module and writes the concatenated output to
// cfg.Bench.BaselinePath.
var benchBaselineCmd = &cobra.Command{
	Use:   "baseline",
	Short: "Record a benchmark baseline",
	Long: "Runs `go test -bench=. -run=^$ -benchmem -count=N` per " +
		"module and writes the captured output to the configured " +
		"baseline path. The file becomes the reference subsequent " +
		"`ergon bench regression` runs compare against.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return bench.Baseline(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, mods, cfg.Test, cfg.Bench)
	},
}

func init() {
	benchCmd.AddCommand(benchBaselineCmd)
}
