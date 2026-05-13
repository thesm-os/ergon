// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/mod"
)

// modTidyCmd is `ergon mod tidy`. Runs `go mod tidy` in each
// discovered module; modifies go.mod and go.sum on disk per Go's
// tidy semantics.
var modTidyCmd = &cobra.Command{
	Use:   "tidy",
	Short: "Run go mod tidy in each module",
	Long: "For each discovered module, runs `go mod tidy`. Updates go.mod " +
		"and go.sum in place; the user is responsible for committing the " +
		"resulting changes.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return mod.Tidy(cmd.Context(), xexec.Command{}, cmd.OutOrStdout(), cmd.ErrOrStderr(), root, mods)
	},
}

// init attaches modTidyCmd under `ergon mod`.
func init() {
	modCmd.AddCommand(modTidyCmd)
}
