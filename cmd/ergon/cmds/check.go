// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	"go.thesmos.sh/ergon/internal/checks/skipexpiry"
	"go.thesmos.sh/ergon/internal/checks/vuln"
	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/lint"
	"go.thesmos.sh/ergon/internal/mod"
	"go.thesmos.sh/ergon/internal/test"
)

// checkCmd is `ergon check`. Bare invocation runs the full
// pre-merge gate: mod verify, lint, test, skip-expiry,
// error-prefix, vuln. Per-stage subcommands attach from their own
// files.
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run the full pre-merge gate",
	Long: "Runs the umbrella check sequence: mod verify, lint, test, " +
		"skip-expiry, error-prefix, and vuln. Each stage's failure " +
		"short-circuits the rest. Subcommands run individual stages.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
		runner := xexec.Command{}

		root, mods, err := discover.Resolve(ctx, cfg.Modules)
		if err != nil {
			return err
		}
		if err = mod.Verify(ctx, runner, stdout, stderr, root, mods); err != nil {
			return err
		}
		if err = lint.All(ctx, runner, stdout, stderr,
			lint.Inputs{Root: root, Modules: mods}, cfg.Markdown, cfg.License); err != nil {
			return err
		}
		in, err := testInputs(ctx)
		if err != nil {
			return err
		}
		if err = test.Run(ctx, runner, stdout, stderr, in, cfg.Test); err != nil {
			return err
		}
		if err = skipexpiry.Run(stdout, stderr, root); err != nil {
			return err
		}
		if err = errorprefix.Run(stdout, stderr, root, cfg.Checks.ErrorPrefix); err != nil {
			return err
		}
		return vuln.Run(ctx, runner, stdout, stderr, root, mods)
	},
}

// init attaches checkCmd to the root. Subcommand files attach
// themselves to checkCmd from their own init() functions.
func init() {
	rootCmd.AddCommand(checkCmd)
}
