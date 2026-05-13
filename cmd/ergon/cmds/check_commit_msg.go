// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/commitmsg"
)

// checkCommitMsgCmd is `ergon check commit-msg <file>`. Validates
// the commit subject in the given file against the configured
// Conventional Commits subset. Pre-commit's `commit-msg` stage
// invokes it with `.git/COMMIT_EDITMSG`.
var checkCommitMsgCmd = &cobra.Command{
	Use:   "commit-msg <file>",
	Short: "Validate a commit message file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return commitmsg.Run(cmd.OutOrStdout(), cmd.ErrOrStderr(),
			args[0], cfg.Checks.CommitMsg)
	},
}

func init() {
	checkCmd.AddCommand(checkCommitMsgCmd)
}
