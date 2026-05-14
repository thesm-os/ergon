// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/skipexpiry"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// lintSkipExpiryCmd is `ergon lint skip-expiry`. Scans every
// git-visible `_test.go` file for `t.Skip("...expires YYYY-MM-DD")`
// declarations and fails when any date is on or before today. The
// command lives under `lint` because the scan is pure static
// analysis — no test execution is required — so it belongs in the
// same umbrella as vet and golangci-lint.
var lintSkipExpiryCmd = &cobra.Command{
	Use:   "skip-expiry",
	Short: "Enforce the t.Skip expiry-date policy",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		root, err := discover.Root(ctx)
		if err != nil {
			return err
		}
		files, err := discover.GitFiles(ctx, xexec.Command{}, root, "_test.go")
		if err != nil {
			return err
		}
		return skipexpiry.Run(cmd.OutOrStdout(), cmd.ErrOrStderr(), root, files)
	},
}

func init() {
	lintCmd.AddCommand(lintSkipExpiryCmd)
}
