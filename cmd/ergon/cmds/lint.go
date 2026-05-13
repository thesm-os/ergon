// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/lint"
)

// lintCmd is `ergon lint`. Running it bare invokes [lint.All]
// (vet + golangci-lint + markdown lint + license verify);
// subcommand files attach per-stage commands to it.
var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Run the full lint suite",
	Long: "Runs go vet, golangci-lint, markdownlint-cli2 (reporting " +
		"only), and `go-license --verify` across the discovered " +
		"modules. Subcommands run individual stages.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return lint.All(
			cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			lint.Inputs{Root: root, Modules: mods},
			cfg.Markdown, cfg.License,
		)
	},
}

// init attaches lintCmd to the root. Per-stage subcommands attach
// from their own init() functions.
func init() {
	rootCmd.AddCommand(lintCmd)
}
