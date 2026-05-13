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

// checkCoverageCmd is `ergon check coverage`. Reads the
// layered-threshold schema, merges per-module `.out` profiles
// under the coverage directory, and fails any function below its
// layer's minimum coverage.
var checkCoverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Enforce per-layer coverage thresholds",
	Long: "Reads the per-layer thresholds from `.ergon.yaml`'s " +
		"`checks.coverage` section, merges the per-module `.out` " +
		"profiles produced by `ergon test`, runs `go tool cover -func`, " +
		"and fails any function below its layer's minimum coverage.",
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
		return coverage.Run(ctx, xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, coverageDir, importPath+"/", cfg.Checks.Coverage)
	},
}

func init() {
	checkCmd.AddCommand(checkCoverageCmd)
}
