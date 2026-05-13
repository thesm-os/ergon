// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/mutation"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// mutationVerbose toggles the "Non-killed mutants" dump in the
// per-target report. Bound to `--mutants` on [checkMutationCmd];
// the root `-v / --verbose` flag is reserved for the
// stream-vs-buffer toggle so it carries its own name here.
var mutationVerbose bool

// checkMutationCmd is `ergon check mutation`. Iterates every
// layer in cfg.Checks.Mutation.Packages (or the subset named by
// positional args), runs `gremlins unleash` per target, and fails
// any layer below its score or coverage threshold.
//
// Positional arguments are either `<layer>` (the layer key, e.g.
// `foundation`) or `<layer>/<subpath>` (restrict gremlins to a
// subtree while keeping the layer's thresholds).
var checkMutationCmd = &cobra.Command{
	Use:   "mutation [target...]",
	Short: "Enforce per-layer mutation thresholds via gremlins",
	Long: "Runs `gremlins unleash` against each layer declared in " +
		"`.ergon.yaml`'s `checks.mutation.packages`. Both the score " +
		"(test efficacy) and coverage (mutator coverage) thresholds " +
		"must pass for the layer to be accepted.\n\n" +
		"Positional arguments restrict the run to specific targets. Each " +
		"argument is either `<layer>` or `<layer>/<subpath>`; the layer " +
		"prefix selects the threshold entry. With no args every declared " +
		"layer is exercised. The --mutants flag dumps every non-killed " +
		"mutant for failing targets so editors can jump to file:line:col.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		root, err := discover.Root(ctx)
		if err != nil {
			return err
		}
		return mutation.Run(ctx, xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, cfg.Checks.Mutation,
			cfg.Checks.Excludes, cfg.Checks.Skips,
			mutation.RunOptions{Targets: args, Verbose: mutationVerbose})
	},
}

func init() {
	checkMutationCmd.Flags().BoolVar(&mutationVerbose, "mutants", false,
		"dump every non-killed mutant (file:line:col) for every failing target")
	checkCmd.AddCommand(checkMutationCmd)
}
