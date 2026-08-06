// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"context"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/config"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testFlags captures the per-invocation overrides for
// `ergon test`. Empty / zero values fall back to `cfg.Test`.
var testFlags struct {
	count   int
	cpu     int
	timeout time.Duration
}

// testCmd is `ergon test`. Running it bare runs `go test ./...`
// per module with coverage; subcommand files swap mode (race,
// bench, fuzz, coverage).
var testCmd = &cobra.Command{
	Use:   "test [pattern]",
	Short: "Run go test per module with coverage",
	Long: "Runs `go test ./...` in every discovered module with the " +
		"knobs from `.ergon.yaml`'s test section (cpu, count, timeout) " +
		"and writes a per-module coverage profile to " +
		"`.<name>/coverage/<module>.out`.\n\nA positional `[pattern]` " +
		"becomes `-run=<pattern>` so a single test (or test set) can " +
		"be exercised. The --count / --cpu / --timeout flags override " +
		"the corresponding `.ergon.yaml` field for this run only.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		in, err := testInputs(cmd.Context())
		if err != nil {
			return err
		}
		return test.Run(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			in, cfg.Test, testOverride(args), stageOpts())
	},
}

// testOverride composes the [test.Override] from the cobra flags
// shared across the test family commands and an optional
// positional pattern.
func testOverride(args []string) test.Override {
	var pattern string
	if len(args) == 1 {
		pattern = args[0]
	}
	return test.Override{
		Pattern: pattern,
		Count:   testFlags.count,
		CPU:     testFlags.cpu,
		Timeout: testFlags.timeout,
	}
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
	imports, err := discover.ModuleImports(root, mods)
	if err != nil {
		return test.Inputs{}, err
	}
	return test.Inputs{
		Root:        root,
		Modules:     mods,
		Imports:     imports,
		CoverageDir: filepath.Join(root, config.ArtifactDir, "coverage"),
	}, nil
}

// init attaches testCmd to the root. Subcommand files attach
// themselves to testCmd from their own init() functions and bind
// their own count / time flags.
func init() {
	testCmd.Flags().IntVar(&testFlags.count, "count", 0,
		"override .ergon.yaml's test.count (-count) for this run")
	testCmd.Flags().IntVar(&testFlags.cpu, "cpu", 0,
		"override .ergon.yaml's test.cpu (-cpu) for this run")
	testCmd.Flags().DurationVar(&testFlags.timeout, "timeout", 0,
		"override .ergon.yaml's test.timeout (-timeout) for this run")
	rootCmd.AddCommand(testCmd)
}
