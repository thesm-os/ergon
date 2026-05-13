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

// init registers persistent flags on rootCmd. Subcommand files add
// themselves to rootCmd from their own init() functions.
func init() {
	rootCmd.PersistentFlags().StringVar(
		&cfgPath, "config", "",
		"path to .ergon.yaml (defaults to repository root)",
	)
}

// Execute runs the ergon CLI. It wires SIGINT and SIGTERM to a
// cancellation context, propagates that context to every subcommand
// (so subprocesses they launch are cancelled on signal), and
// returns the first error any command produced.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}
