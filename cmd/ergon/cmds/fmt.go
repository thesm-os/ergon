// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/format"
)

// fmtCmd is `ergon fmt`. Applies license headers, then runs
// gofumpt + gci per module, then markdownlint-cli2 across the
// workspace.
var fmtCmd = &cobra.Command{
	Use:   "fmt",
	Short: "Format Go and Markdown sources",
	Long: "Applies SPDX license headers, runs gofumpt and gci per " +
		"module to format Go sources, then runs markdownlint-cli2 " +
		"across the workspace's Markdown files.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		importPath, err := discover.ImportPath(root)
		if err != nil {
			return err
		}
		return format.Run(
			cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			format.Inputs{Root: root, ImportPath: importPath, Modules: mods},
			cfg.License, cfg.Markdown, stageOpts(),
		)
	},
}

// init attaches fmtCmd to the root.
func init() {
	rootCmd.AddCommand(fmtCmd)
}
