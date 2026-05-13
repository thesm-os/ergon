// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// checkCoverageUncoveredFlags captures the local flags on
// `check coverage uncovered`. --all flips the report from
// policy-filtered (the default) to "show every uncovered block".
var checkCoverageUncoveredFlags struct {
	all bool
}

// checkCoverageUncoveredCmd is `ergon check coverage uncovered`.
// Renders every uncovered block grouped by file → function. By
// default the report respects the same policy the gate command
// applies (layer membership + `checks.excludes` + `checks.skips`);
// `--all` removes the filters so the full coverage debt surfaces.
var checkCoverageUncoveredCmd = &cobra.Command{
	Use:   "uncovered",
	Short: "List every uncovered line, grouped by file → function",
	Long: "Maps every count=0 block in the merged coverprofile to its " +
		"containing function and prints the result grouped by file. " +
		"By default the report respects the same policy the gate " +
		"command applies: only functions under " +
		"`checks.coverage.packages` layers, minus `checks.excludes` " +
		"and `checks.skips`. Pass --all to disable the filters and " +
		"show every uncovered block across every package " +
		"`ergon test` touched.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		root, mods, err := discover.Resolve(ctx, cfg.Modules)
		if err != nil {
			return err
		}
		imports, err := discover.ModuleImports(root, mods)
		if err != nil {
			return err
		}
		name := cfg.Name
		if name == "" {
			name = filepath.Base(root)
		}
		coverageDir := filepath.Join(root, "."+name, "coverage")
		return coverage.Uncovered(ctx, xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, coverageDir, imports,
			cfg.Checks.Coverage, cfg.Checks.Excludes, cfg.Checks.Skips,
			coverage.UncoveredOptions{All: checkCoverageUncoveredFlags.all})
	},
}

func init() {
	checkCoverageUncoveredCmd.Flags().BoolVar(
		&checkCoverageUncoveredFlags.all, "all", false,
		"Skip the policy filters and show every uncovered block",
	)
	checkCoverageCmd.AddCommand(checkCoverageUncoveredCmd)
}
