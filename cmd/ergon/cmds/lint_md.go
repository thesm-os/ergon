// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/markdown"
)

// lintMdCmd is `ergon lint md`. Runs markdownlint-cli2 in
// reporting mode against the configured glob list.
var lintMdCmd = &cobra.Command{
	Use:   "md",
	Short: "Run markdownlint-cli2 (reporting only)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := discover.Root(cmd.Context())
		if err != nil {
			return err
		}
		return markdown.Lint(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), root, cfg.Markdown)
	},
}

func init() {
	lintCmd.AddCommand(lintMdCmd)
}
