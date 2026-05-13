// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/lint"
)

// lintGoCmd is `ergon lint go`. Runs `golangci-lint run ./...` per
// module. golangci-lint reads its own `.golangci.yml`; ergon
// passes no extra flags.
var lintGoCmd = &cobra.Command{
	Use:   "go",
	Short: "Run golangci-lint per module",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return lint.Go(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			lint.Inputs{Root: root, Modules: mods})
	},
}

func init() {
	lintCmd.AddCommand(lintGoCmd)
}
