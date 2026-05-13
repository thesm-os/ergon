// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testCoverageCmd is `ergon test coverage`. Converts the per-module
// `.out` profiles produced by `ergon test` into HTML reports via
// `go tool cover`.
var testCoverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Generate HTML reports from existing coverage profiles",
	Long: "For each module with a `<name>.out` profile under the " +
		"coverage directory, runs `go tool cover -html` and writes a " +
		"sibling `<name>.html`. Run `ergon test` first to produce the " +
		"profiles.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		in, err := testInputs(cmd.Context())
		if err != nil {
			return err
		}
		return test.Coverage(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), in, stageOpts())
	},
}

func init() {
	testCmd.AddCommand(testCoverageCmd)
}
