// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/skipexpiry"
	"go.thesmos.sh/ergon/internal/discover"
)

// checkSkipExpiryCmd is `ergon check skip-expiry`. Walks the
// working tree for `t.Skip("...expires YYYY-MM-DD")` declarations
// and fails when any date is on or before today.
var checkSkipExpiryCmd = &cobra.Command{
	Use:   "skip-expiry",
	Short: "Enforce the t.Skip expiry-date policy",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := discover.Root(cmd.Context())
		if err != nil {
			return err
		}
		return skipexpiry.Run(cmd.OutOrStdout(), cmd.ErrOrStderr(), root)
	},
}

func init() {
	checkCmd.AddCommand(checkSkipExpiryCmd)
}
