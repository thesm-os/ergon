// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/mod"
)

// modInstallCmd is `ergon mod install`. Runs `go mod download` and
// `go mod verify` per discovered module.
var modInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Download and verify module dependencies",
	Long: "For each discovered module, runs `go mod download` (fills the " +
		"module cache) then `go mod verify` (checks downloaded modules " +
		"against go.sum).",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return mod.Install(
			cmd.Context(),
			xexec.Command{},
			cmd.OutOrStdout(),
			cmd.ErrOrStderr(),
			root,
			mods,
			stageOpts(),
		)
	},
}

// init attaches modInstallCmd under `ergon mod`.
func init() {
	modCmd.AddCommand(modInstallCmd)
}
