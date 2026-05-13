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

// checkCoverageUncoveredCmd is `ergon check coverage uncovered`.
// Lists every uncovered line range in the merged coverprofile,
// regardless of `checks.coverage.packages`, `checks.excludes`,
// or `checks.skips`. The gate command (`ergon check coverage`)
// reports only the layers a project explicitly declares; this
// subcommand surfaces every uncovered block across every package
// the test suite touched, so contributors can see the full debt
// when closing gaps.
var checkCoverageUncoveredCmd = &cobra.Command{
	Use:   "uncovered",
	Short: "List every uncovered line across the whole tree",
	Long: "Lists every count=0 block in the merged coverprofile, " +
		"grouped by file. Ignores `checks.coverage.packages`, " +
		"`checks.excludes`, and `checks.skips` — the report shows " +
		"every uncovered region across every package `ergon test` " +
		"touched, not just the configured threshold layers. Run " +
		"`ergon test` first to produce the profiles.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		root, err := discover.Root(ctx)
		if err != nil {
			return err
		}
		importPath, err := discover.ImportPath(root)
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
			root, coverageDir, importPath+"/")
	},
}

func init() {
	checkCoverageCmd.AddCommand(checkCoverageUncoveredCmd)
}
