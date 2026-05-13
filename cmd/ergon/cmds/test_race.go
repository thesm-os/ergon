// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testRaceCmd is `ergon test race`. Runs `go test -race ./...`
// per module with the configured race count.
var testRaceCmd = &cobra.Command{
	Use:   "race",
	Short: "Run go test -race per module",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		in, err := testInputs(cmd.Context())
		if err != nil {
			return err
		}
		return test.Race(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), in, cfg.Test)
	},
}

func init() {
	testCmd.AddCommand(testRaceCmd)
}
