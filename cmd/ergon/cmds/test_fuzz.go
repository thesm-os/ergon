// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testFuzzCmd is `ergon test fuzz`. Discovers every Fuzz* target
// across the modules and runs each sequentially for the configured
// fuzz_time.
var testFuzzCmd = &cobra.Command{
	Use:   "fuzz",
	Short: "Discover and run every Fuzz* target",
	Long: "Walks the working tree for `func Fuzz*` declarations and runs " +
		"each via `go test -run=^$ -fuzz=^Name$ -fuzztime=...`. Each " +
		"target gets the budget from `.ergon.yaml`'s test.fuzz_time.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		in, err := testInputs(cmd.Context())
		if err != nil {
			return err
		}
		return test.Fuzz(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), in, cfg.Test)
	},
}

func init() {
	testCmd.AddCommand(testFuzzCmd)
}
