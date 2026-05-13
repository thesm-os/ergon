// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
)

// modListCmd is `ergon mod list`. It resolves the module set the
// same way every other multi-module command does and prints the
// discovered directories one per line. Used as a debugging aid to
// confirm `go.work` / `.ergon.yaml` are being interpreted as the
// user expects.
var modListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print discovered modules",
	Long: "Resolve the module set from `.ergon.yaml`'s `modules` " +
		"override or `go.work`, then print one directory per line. " +
		"Root is printed as `.`.",
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
		out := cmd.OutOrStdout()
		for _, m := range mods {
			fmt.Fprintln(out, m.Dir)
		}
		return nil
	},
}

// init attaches modListCmd under `ergon mod`.
func init() {
	modCmd.AddCommand(modListCmd)
}
