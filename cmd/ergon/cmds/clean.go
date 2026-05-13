// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/clean"
	"go.thesmos.sh/ergon/internal/discover"
)

// cleanCmd is `ergon clean`. Removes the build and coverage
// artefact directories ergon's other commands write
// (`bin/`, `.<name>/`, `dist/`).
var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove build and coverage artefacts",
	Long: "Removes `bin/`, `.<name>/` (e.g. `.ergon/`), and `dist/` " +
		"under the repository root. Does not touch the Go module " +
		"cache or test cache — `go clean` owns those.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := discover.Root(cmd.Context())
		if err != nil {
			return err
		}
		name := cfg.Name
		if name == "" {
			name = filepath.Base(root)
		}
		return clean.Run(cmd.OutOrStdout(), root, name)
	},
}

func init() {
	rootCmd.AddCommand(cleanCmd)
}
