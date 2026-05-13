// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"time"

	"github.com/spf13/cobra"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testBenchFlags carry the per-invocation overrides on
// `ergon test bench`. --count maps to cfg.BenchCount; --time
// becomes `-benchtime` (Go's per-benchmark duration).
var testBenchFlags struct {
	count   int
	time    time.Duration
	timeout time.Duration
}

// testBenchCmd is `ergon test bench`. Runs `go test -bench=<pattern>
// -run=^$ -benchmem ./...` per module. A positional `[pattern]`
// defaults to `.` (every benchmark).
var testBenchCmd = &cobra.Command{
	Use:   "bench [pattern]",
	Short: "Run benchmarks per module",
	Long: "Runs `go test -bench=<pattern> -run=^$ -benchmem ./...` per " +
		"module. A positional `[pattern]` becomes `-bench=<pattern>` " +
		"(default `.`); --count shadows `test.bench_count`; --time " +
		"becomes `-benchtime` (omitted when zero, which keeps Go's " +
		"default of 1s).",
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
		return test.Bench(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			in, cfg.Test, test.Override{
				Pattern: pattern,
				Count:   testBenchFlags.count,
				Time:    testBenchFlags.time,
				Timeout: testBenchFlags.timeout,
			}, stageOpts())
	},
}

func init() {
	testBenchCmd.Flags().IntVar(&testBenchFlags.count, "count", 0,
		"override .ergon.yaml's test.bench_count (-count) for this run")
	testBenchCmd.Flags().DurationVar(&testBenchFlags.time, "time", 0,
		"-benchtime per benchmark (default: Go's 1s)")
	testBenchCmd.Flags().DurationVar(&testBenchFlags.timeout, "timeout", 0,
		"override .ergon.yaml's test.timeout (-timeout) for this run")
	testCmd.AddCommand(testBenchCmd)
}
