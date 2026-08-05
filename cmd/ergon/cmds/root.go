// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package cmds defines the cobra command tree for the ergon CLI.
// Each subcommand lives in its own file and registers itself with
// [rootCmd] via init(); this package's files contain command
// definitions and flag wiring only. Every command's RunE delegates
// to the corresponding `internal/<subsystem>` package for the
// actual work.
package cmds

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/config"
	"go.thesmos.sh/ergon/internal/runtmp"
	"go.thesmos.sh/ergon/internal/version"
)

// rootCmd is the top-level `ergon` command. Subcommand files
// register themselves against it from init().
var rootCmd = &cobra.Command{
	Use:   "ergon",
	Short: "Task runner for Go projects",
	Long: "ergon runs lifecycle tasks for Go projects: format, lint, " +
		"test, benchmark, release. It reads `go.work` (or walks for " +
		"`go.mod` files) and runs each task against the discovered " +
		"module set.",
	Version:      version.Full(),
	SilenceUsage: true,
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		loaded, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		cfg = loaded
		return nil
	},
}

// cfg holds the resolved configuration for the current invocation.
// rootCmd's PersistentPreRunE populates it before any subcommand's
// RunE executes; subcommand RunE functions read it directly.
var cfg config.Config

// cfgPath captures --config. An empty value means "discover
// `.ergon.yaml` from the current directory."
var cfgPath string

// fastMode captures --fast / -f. When true, every gate command
// (lint, test, build, mod, check, vuln) short-circuits at the
// first per-module or per-stage failure instead of running every
// target and reporting an aggregated summary. The default (full
// run) is more useful for CI; the fast flag exists for the dev
// loop where one wants to fix the first failure and move on.
var fastMode bool

// verboseMode captures --verbose / -v. By default the gate
// subsystems run the underlying tool with stdout/stderr captured
// to a buffer; the buffer is shown only when the command fails,
// indented and dimmed beneath the per-module verdict line. When
// true, the raw tool output is streamed live as it always used to
// be, so users can watch long-running operations (e.g. `go test`)
// in real time.
var verboseMode bool

// init registers persistent flags on rootCmd. Subcommand files add
// themselves to rootCmd from their own init() functions.
func init() {
	rootCmd.PersistentFlags().StringVar(
		&cfgPath, "config", "",
		"path to .ergon.yaml (defaults to repository root)",
	)
	rootCmd.PersistentFlags().BoolVarP(
		&fastMode, "fast", "f", false,
		"fail-fast: stop at the first per-module or per-stage failure (default: run every target)",
	)
	rootCmd.PersistentFlags().BoolVarP(
		&verboseMode, "verbose", "v", false,
		"stream the underlying tool's output live (default: buffered, revealed only on failure)",
	)
}

// runTmp is this invocation's private temp root, published through
// TMPDIR by [runtmp.New]. `ergon clean` reads it so a sweep of
// abandoned roots does not reclaim the one it is running inside.
var runTmp string

// Execute runs the ergon CLI. It wires SIGINT and SIGTERM to a
// cancellation context, propagates that context to every subcommand
// (so subprocesses they launch are cancelled on signal), and
// returns the first error any command produced.
//
// The per-run temp root is established here rather than in a
// PersistentPreRunE for two reasons: it must exist before any
// command body runs, and [runtmp.New] calls os.Setenv, which races
// concurrent os.Environ reads — the branch gate reads the
// environment from a worker pool. Here there is provably no other
// goroutine yet.
//
// The cleanup is deferred, so it covers a normal exit and the
// SIGINT / SIGTERM path above. A SIGKILL still leaks the root;
// `ergon clean` reclaims those.
func Execute() error {
	root, cleanup, err := runtmp.New()
	if err != nil {
		return err
	}
	runTmp = root
	defer cleanup()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}
