// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/vuln"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// checkVulnCmd is `ergon check vuln`. Runs `govulncheck ./...`
// per discovered module.
var checkVulnCmd = &cobra.Command{
	Use:   "vuln",
	Short: "Run govulncheck per module",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return vuln.Run(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), root, mods, fastMode)
	},
}

func init() {
	checkCmd.AddCommand(checkVulnCmd)
}
