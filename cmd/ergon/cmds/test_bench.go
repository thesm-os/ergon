// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testBenchCmd is `ergon test bench`. Runs `go test -bench=. -run=^$
// -benchmem ./...` per module.
var testBenchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Run benchmarks per module",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		in, err := testInputs(cmd.Context())
		if err != nil {
			return err
		}
		return test.Bench(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), in, cfg.Test, stageOpts())
	},
}

func init() {
	testCmd.AddCommand(testBenchCmd)
}
