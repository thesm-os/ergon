// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
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
		root, err := discover.Root(cmd.Context())
		if err != nil {
			return err
		}
		mods, err := discover.Modules(root, cfg.Modules)
		if err != nil {
			return err
		}
		return mod.Install(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), root, mods)
	},
}

// init attaches modInstallCmd under `ergon mod`.
func init() {
	modCmd.AddCommand(modInstallCmd)
}
