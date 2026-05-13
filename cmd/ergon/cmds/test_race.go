// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"time"

	"github.com/spf13/cobra"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testRaceFlags carry the per-invocation overrides on
// `ergon test race`. Race uses cfg.RaceCount instead of
// cfg.Count, so --count here shadows RaceCount.
var testRaceFlags struct {
	count   int
	cpu     int
	timeout time.Duration
}

// testRaceCmd is `ergon test race`. Runs `go test -race ./...`
// per module with the configured race count.
var testRaceCmd = &cobra.Command{
	Use:   "race [pattern]",
	Short: "Run go test -race per module",
	Long: "Runs `go test -race ./...` per module with the configured " +
		"race-count + timeout. A positional `[pattern]` becomes " +
		"`-run=<pattern>`; --count shadows .ergon.yaml's " +
		"test.race_count for this run.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		in, err := testInputs(cmd.Context())
		if err != nil {
			return err
		}
		var pattern string
		if len(args) == 1 {
			pattern = args[0]
		}
		return test.Race(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			in, cfg.Test, test.Override{
				Pattern: pattern,
				Count:   testRaceFlags.count,
				CPU:     testRaceFlags.cpu,
				Timeout: testRaceFlags.timeout,
			}, stageOpts())
	},
}

func init() {
	testRaceCmd.Flags().IntVar(&testRaceFlags.count, "count", 0,
		"override .ergon.yaml's test.race_count (-count) for this run")
	testRaceCmd.Flags().IntVar(&testRaceFlags.cpu, "cpu", 0,
		"override .ergon.yaml's test.cpu (-cpu) for this run")
	testRaceCmd.Flags().DurationVar(&testRaceFlags.timeout, "timeout", 0,
		"override .ergon.yaml's test.timeout (-timeout) for this run")
	testCmd.AddCommand(testRaceCmd)
}
