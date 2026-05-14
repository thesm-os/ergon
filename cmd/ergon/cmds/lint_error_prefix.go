// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// lintErrorPrefixCmd is `ergon lint error-prefix`. Enforces the
// `errors.New("<pkg>: ...")` convention across every git-visible
// non-test Go source file. The command lives under `lint` because
// the scan is pure static analysis (AST-based, no compilation or
// test execution), grouping it with vet and golangci-lint.
var lintErrorPrefixCmd = &cobra.Command{
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
			root, files, cfg.Lint.ErrorPrefix)
	},
}

func init() {
	lintCmd.AddCommand(lintErrorPrefixCmd)
}
