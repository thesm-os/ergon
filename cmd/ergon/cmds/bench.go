// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"
)

// benchCmd groups the benchmark-management subcommands (baseline
// + regression). Running it bare prints help; real work lives in
// the subcommands.
var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Benchmark lifecycle (baseline + regression)",
	Long: "`ergon bench baseline` records a pinned reference run to " +
		"`bench/baseline.txt`. `ergon bench regression` runs the suite " +
		"again and diffs the new sample against the baseline via " +
		"`benchstat`. Sample count and timeout come from the `test:` " +
		"section of `.ergon.yaml`.",
}

func init() {
	rootCmd.AddCommand(benchCmd)
}
