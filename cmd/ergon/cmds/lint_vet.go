// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/lint"
)

// lintVetCmd is `ergon lint vet`. Runs `go vet ./...` per module.
var lintVetCmd = &cobra.Command{
	Use:   "vet",
	Short: "Run `go vet ./...` per module",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return lint.Vet(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			lint.Inputs{Root: root, Modules: mods}, stageOpts())
	},
}

func init() {
	lintCmd.AddCommand(lintVetCmd)
}
