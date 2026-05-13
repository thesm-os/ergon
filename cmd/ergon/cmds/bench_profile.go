// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/bench"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// benchProfileFlags captures the raw cobra flag values for
// `ergon bench profile`. The struct exists to keep the cobra
// init() declaration self-contained — every variable bound below
// is a field here.
var benchProfileFlags struct {
	module    string
	packages  string
	benchTime time.Duration
	count     int
	cpu       bool
	mem       bool
	block     bool
	mutex     bool
	outputDir string
	topN      int
}

// benchProfileCmd is `ergon bench profile [pattern]`. Runs
// `go test -bench` with one or more pprof profile flags enabled
// and writes the artefacts under `<name>/profiles/` (or the
// caller-supplied --output-dir).
//
// Profile is interactive: it targets a single module + package
// pattern (default: root module, `./...`). The per-module
// fan-out used by [Baseline] and [Regression] does not apply —
// profiling every module rarely matches the developer's intent.
var benchProfileCmd = &cobra.Command{
	Use:   "profile [pattern]",
	Short: "Collect pprof artefacts (CPU/mem/block/mutex) for a benchmark",
	Long: "Runs `go test -bench=<pattern> -run=^$` against the configured " +
		"module + package set with one or more pprof profile flags set " +
		"and writes the artefacts to `<name>/profiles/`. The summary " +
		"prints the `go tool pprof -http=: <path>` command that opens " +
		"each artefact in pprof's web UI.\n\n" +
		"Default profiles are CPU + memory; pass --block / --mutex to " +
		"collect those too (both add runtime overhead). Pass --cpu=false " +
		"or --mem=false to disable an individual default.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		root, err := discover.Root(ctx)
		if err != nil {
			return err
		}
		pattern := "."
		if len(args) == 1 {
			pattern = args[0]
		}
		outputDir := benchProfileFlags.outputDir
		if outputDir == "" {
			name := cfg.Name
			if name == "" {
				name = filepath.Base(root)
			}
			outputDir = filepath.Join(root, "."+name, "profiles")
		}
		return bench.Profile(ctx, xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, bench.ProfileOptions{
				Pattern:   pattern,
				Module:    benchProfileFlags.module,
				Packages:  benchProfileFlags.packages,
				BenchTime: benchProfileFlags.benchTime,
				Count:     benchProfileFlags.count,
				CPU:       benchProfileFlags.cpu,
				Mem:       benchProfileFlags.mem,
				Block:     benchProfileFlags.block,
				Mutex:     benchProfileFlags.mutex,
				OutputDir: outputDir,
				TopN:      benchProfileFlags.topN,
			})
	},
}

func init() {
	benchProfileCmd.Flags().StringVar(&benchProfileFlags.module, "module", ".",
		"Module directory to profile inside (relative to repository root)")
	benchProfileCmd.Flags().StringVar(&benchProfileFlags.packages, "package", "./...",
		"Package pattern within the module")
	benchProfileCmd.Flags().DurationVar(&benchProfileFlags.benchTime, "time", bench.DefaultBenchTime,
		"`-benchtime` for each benchmark run")
	benchProfileCmd.Flags().IntVar(&benchProfileFlags.count, "count", 1,
		"`-count` for each benchmark run")
	benchProfileCmd.Flags().BoolVar(&benchProfileFlags.cpu, "cpu", true,
		"Collect CPU profile (-cpuprofile)")
	benchProfileCmd.Flags().BoolVar(&benchProfileFlags.mem, "mem", true,
		"Collect memory profile (-memprofile + -benchmem)")
	benchProfileCmd.Flags().BoolVar(&benchProfileFlags.block, "block", false,
		"Collect block profile (-blockprofile); adds runtime overhead")
	benchProfileCmd.Flags().BoolVar(&benchProfileFlags.mutex, "mutex", false,
		"Collect mutex profile (-mutexprofile); adds runtime overhead")
	benchProfileCmd.Flags().StringVar(&benchProfileFlags.outputDir, "output-dir", "",
		"Directory for the collected profiles (default: <root>/.<name>/profiles)")
	benchProfileCmd.Flags().IntVar(&benchProfileFlags.topN, "top", bench.DefaultProfileTopN,
		"How many rows of `go tool pprof -top` to summarize per artefact")
	benchCmd.AddCommand(benchProfileCmd)
}
