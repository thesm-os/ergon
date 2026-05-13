// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"time"

	"github.com/spf13/cobra"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/test"
)

// testFuzzFlags carry the per-invocation overrides on
// `ergon test fuzz`. --time shadows cfg.FuzzTime; the positional
// `[pattern]` regex filters discovered Fuzz targets by name.
var testFuzzFlags struct {
	time    time.Duration
	timeout time.Duration
}

// testFuzzCmd is `ergon test fuzz`. Discovers every Fuzz* target
// across the modules and runs each sequentially for the
// configured fuzz_time.
var testFuzzCmd = &cobra.Command{
	Use:   "fuzz [pattern]",
	Short: "Discover and run every Fuzz* target",
	Long: "Walks the working tree for `func Fuzz*` declarations and runs " +
		"each via `go test -run=^$ -fuzz=^Name$ -fuzztime=...`. A " +
		"positional `[pattern]` is a regex matched against discovered " +
		"target names so a single target (or subset) can be exercised. " +
		"--time shadows `.ergon.yaml`'s test.fuzz_time for this run.",
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
		return test.Fuzz(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			in, cfg.Test, test.Override{
				Pattern: pattern,
				Time:    testFuzzFlags.time,
				Timeout: testFuzzFlags.timeout,
			})
	},
}

func init() {
	testFuzzCmd.Flags().DurationVar(&testFuzzFlags.time, "time", 0,
		"override .ergon.yaml's test.fuzz_time (-fuzztime) for this run")
	testFuzzCmd.Flags().DurationVar(&testFuzzFlags.timeout, "timeout", 0,
		"override .ergon.yaml's test.timeout (-timeout) for this run")
	testCmd.AddCommand(testFuzzCmd)
}
