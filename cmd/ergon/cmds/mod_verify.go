// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	"go.thesmos.sh/ergon/internal/mod"
)

// modVerifyCmd is `ergon mod verify`. Runs `go mod tidy` per
// module, then fails if any go.mod or go.sum would change — the
// CI gate that catches drifted module manifests.
var modVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Fail when go.mod or go.sum would change after `go mod tidy`",
	Long: "Runs `go mod tidy` in every discovered module, then checks each " +
		"module's go.mod and go.sum with `git diff --quiet`. Exits " +
		"non-zero (and names every dirty module) when any tracked file " +
		"would change.",
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
		return mod.Verify(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), root, mods)
	},
}

// init attaches modVerifyCmd under `ergon mod`.
func init() {
	modCmd.AddCommand(modVerifyCmd)
}
