// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/mutation"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// checkMutationCmd is `ergon check mutation`. Iterates every
// layer in cfg.Checks.Mutation.Packages, runs `gremlins unleash`
// per layer, and fails any layer below its score or coverage
// threshold.
var checkMutationCmd = &cobra.Command{
	Use:   "mutation",
	Short: "Enforce per-layer mutation thresholds via gremlins",
	Long: "Runs `gremlins unleash` against each layer declared in " +
		"`.ergon.yaml`'s `checks.mutation.packages`. Both the score " +
		"(test efficacy) and coverage (mutator coverage) thresholds " +
		"must pass for the layer to be accepted.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		root, err := discover.Root(ctx)
		if err != nil {
			return err
		}
		return mutation.Run(ctx, xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, cfg.Checks.Mutation)
	},
}

func init() {
	checkCmd.AddCommand(checkMutationCmd)
}
