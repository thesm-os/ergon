// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/branch"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// checkBranchFlags groups the local flags `ergon check branch`
// exposes. --workers caps the per-package gobco fan-out — zero
// means use the runtime default (NumCPU / 2).
var checkBranchFlags struct {
	workers int
}

// checkBranchCmd is `ergon check branch`. Per-layer branch-
// coverage gating backed by `gobco`. Runs one gobco invocation
// per workspace package (gobco does not accept multi-package
// args), aggregates conditions per layer via the same prefix-
// claim rule the line-coverage gate uses, and gates each layer
// where `require_branch: true` against its declared `branch:`
// threshold.
//
// Not part of the `ergon check` umbrella: gobco rebuilds each
// package under test, so a workspace-wide run is several minutes.
// Use `ergon check branch` explicitly when you want a verdict.
var checkBranchCmd = &cobra.Command{
	Use:   "branch [target...]",
	Short: "Enforce per-layer branch-coverage thresholds (gobco)",
	Long: "Runs `gobco -branch` once per workspace package, " +
		"aggregates conditions by their longest-prefix declared " +
		"layer in `.ergon.yaml`'s `checks.coverage` section, and " +
		"compares each layer's branch-coverage percentage to the " +
		"`branch:` threshold. Layers with `require_branch: true` " +
		"fail the run when below; informational layers render the " +
		"number without failing.\n\n" +
		"Positional arguments restrict the run to specific layer " +
		"prefixes; with none, every configured layer is exercised.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		root, mods, err := discover.Resolve(ctx, cfg.Modules)
		if err != nil {
			return err
		}
		imports, err := discover.ModuleImports(root, mods)
		if err != nil {
			return err
		}
		return branch.Run(ctx, xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, imports, cfg.Checks.Coverage,
			cfg.Checks.Excludes, cfg.Checks.Skips,
			branch.RunOptions{Targets: args, Workers: checkBranchFlags.workers})
	},
}

func init() {
	checkBranchCmd.Flags().IntVar(&checkBranchFlags.workers, "workers", 0,
		"max concurrent gobco invocations (0 = NumCPU/2)")
	checkCmd.AddCommand(checkBranchCmd)
}
