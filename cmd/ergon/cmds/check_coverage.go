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

// coverageVerbose toggles the "Uncovered ranges" dump in the
// per-target report. Bound to `--ranges` on [checkCoverageCmd];
// the root `-v / --verbose` flag is reserved for the
// stream-vs-buffer toggle so it carries its own name here.
var coverageVerbose bool

// checkCoverageCmd is `ergon check coverage`. Reads the
// layered-threshold schema, merges per-module `.out` profiles
// under the coverage directory, and fails any function below its
// layer's minimum coverage.
//
// Positional arguments restrict the run to a subset of the
// configured layers — each arg is matched as a layer prefix
// (e.g. `core/kernel/fold`). With no args every layer in
// `checks.coverage.packages` runs.
var checkCoverageCmd = &cobra.Command{
	Use:   "coverage [target...]",
	Short: "Enforce per-layer coverage thresholds",
	Long: "Reads the per-layer thresholds from `.ergon.yaml`'s " +
		"`checks.coverage` section, merges the per-module `.out` " +
		"profiles produced by `ergon test`, runs `go tool cover -func`, " +
		"and fails any function below its layer's minimum coverage.\n\n" +
		"Positional arguments restrict the run to specific layer prefixes; " +
		"with none, every configured layer is exercised. The --ranges flag " +
		"appends the uncovered block ranges for every failing target.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
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
		return coverage.Run(ctx, xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, coverageDir, importPath+"/", cfg.Checks.Coverage,
			cfg.Checks.Excludes, cfg.Checks.Skips,
			coverage.RunOptions{Targets: args, Verbose: coverageVerbose})
	},
}

func init() {
	checkCoverageCmd.Flags().BoolVar(&coverageVerbose, "ranges", false,
		"dump uncovered block ranges (file:start-end (stmts)) for every failing target")
	checkCmd.AddCommand(checkCoverageCmd)
}
