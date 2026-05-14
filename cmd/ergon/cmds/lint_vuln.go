// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/vuln"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// lintVulnCmd is `ergon lint vuln`. Runs `govulncheck ./...` per
// discovered module. The command lives under `lint` because
// govulncheck is a static analyser (it traces import graphs and
// reachable symbols, not runtime behaviour), grouping it with vet
// and golangci-lint.
var lintVulnCmd = &cobra.Command{
	Use:   "vuln",
	Short: "Run govulncheck per module",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return vuln.Run(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), root, mods, stageOpts())
	},
}

func init() {
	lintCmd.AddCommand(lintVulnCmd)
}
