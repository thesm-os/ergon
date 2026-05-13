// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"context"
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testCmd is `ergon test`. Running it bare runs `go test ./...`
// per module with coverage; subcommand files swap mode (race,
// bench, fuzz, coverage).
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run go test per module with coverage",
	Long: "Runs `go test ./...` in every discovered module with the " +
		"knobs from `.ergon.yaml`'s test section (cpu, count, timeout) " +
		"and writes a per-module coverage profile to " +
		"`.<name>/coverage/<module>.out`.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		in, err := testInputs(cmd.Context())
		if err != nil {
			return err
		}
		return test.Run(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), in, cfg.Test)
	},
}

// testInputs resolves the discovery results and the coverage
// directory the test family commands share. Coverage directory is
// derived from cfg.Name (falling back to the repository's basename)
// so different ecosystems can keep their `.foo/coverage/` layout.
func testInputs(ctx context.Context) (test.Inputs, error) {
	root, mods, err := discover.Resolve(ctx, cfg.Modules)
	if err != nil {
		return test.Inputs{}, err
	}
	name := cfg.Name
	if name == "" {
		name = filepath.Base(root)
	}
	return test.Inputs{
		Root:        root,
		Modules:     mods,
		CoverageDir: filepath.Join(root, "."+name, "coverage"),
	}, nil
}

// init attaches testCmd to the root. Subcommand files attach
// themselves to testCmd from their own init() functions.
func init() {
	rootCmd.AddCommand(testCmd)
}
