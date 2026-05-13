// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"
)

// modCmd groups the multi-module hygiene subcommands (`list`,
// `install`, `tidy`, `verify`). Running it bare prints help; the
// real work lives in its subcommands.
var modCmd = &cobra.Command{
	Use:   "mod",
	Short: "Multi-module hygiene",
	Long: "Subcommands operate across every module discovered in `go.work` " +
		"(or the override list in `.ergon.yaml`). Use `ergon mod list` to " +
		"inspect the resolved set.",
}

// init attaches modCmd to the root. Mod subcommands attach to
// modCmd from their own init() functions.
func init() {
	rootCmd.AddCommand(modCmd)
}
