// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/bootstrap"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// bootstrapCmd is `ergon bootstrap`. The actual install work lives
// in [bootstrap.Run]; this definition only wires the cobra surface.
var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Install development tools",
	Long: "Install the Go tools ergon shells out to: gofumpt, gci, " +
		"golangci-lint, govulncheck, go-license, benchstat, gremlins. " +
		"Also checks for markdownlint-cli2 and tries to install it via " +
		"npm when missing.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return bootstrap.Run(
			cmd.Context(),
			xexec.Command{},
			cmd.OutOrStdout(),
			cmd.ErrOrStderr(),
			cfg.Bootstrap,
		)
	},
}

// init attaches bootstrapCmd to the root.
func init() {
	rootCmd.AddCommand(bootstrapCmd)
}
