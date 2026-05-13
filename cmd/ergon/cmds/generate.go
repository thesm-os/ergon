// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/format"
	"go.thesmos.sh/ergon/internal/generate"
)

// generateCmd is `ergon generate`. Runs `go generate ./...` per
// module then re-runs the format pipeline so the freshly-emitted
// source picks up the project's style and SPDX headers.
var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Run `go generate ./...` per module + format",
	Long: "Re-runs every `//go:generate` directive in the workspace, " +
		"then runs `ergon fmt` so the new files match the repository's " +
		"style and license-header conventions.",
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
		return generate.Run(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			format.Inputs{Root: root, ImportPath: importPath, Modules: mods},
			cfg.License, cfg.Markdown)
	},
}

func init() {
	rootCmd.AddCommand(generateCmd)
}
