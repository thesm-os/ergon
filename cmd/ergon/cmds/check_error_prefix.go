// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	"go.thesmos.sh/ergon/internal/discover"
)

// checkErrorPrefixCmd is `ergon check error-prefix`. Enforces the
// `errors.New("<pkg>: ...")` convention.
var checkErrorPrefixCmd = &cobra.Command{
	Use:   "error-prefix",
	Short: "Enforce the errors.New package-prefix convention",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := discover.Root(cmd.Context())
		if err != nil {
			return err
		}
		return errorprefix.Run(cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, cfg.Checks.ErrorPrefix)
	},
}

func init() {
	checkCmd.AddCommand(checkErrorPrefixCmd)
}
