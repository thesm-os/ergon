// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// checkErrorPrefixCmd is `ergon check error-prefix`. Enforces the
// `errors.New("<pkg>: ...")` convention across every git-visible
// non-test Go source file.
var checkErrorPrefixCmd = &cobra.Command{
	Use:   "error-prefix",
	Short: "Enforce the errors.New package-prefix convention",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		root, err := discover.Root(ctx)
		if err != nil {
			return err
		}
		files, err := discover.GitFiles(ctx, xexec.Command{}, root, ".go")
		if err != nil {
			return err
		}
		return errorprefix.Run(cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, files, cfg.Checks.ErrorPrefix)
	},
}

func init() {
	checkCmd.AddCommand(checkErrorPrefixCmd)
}
