// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/build"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// buildCmd is `ergon build`. Runs `go build ./...` per discovered
// module. Compile-check only — no binaries are written; use
// `goreleaser` for stamped release builds.
var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Compile every module's source (sanity check)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return build.Run(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), root, mods)
	},
}

func init() {
	rootCmd.AddCommand(buildCmd)
}
