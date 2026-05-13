// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/coverage"
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
// pre-merge gate: mod verify → lint → test (which produces the
// coverage profiles) → coverage thresholds → skip-expiry →
// error-prefix → vuln. Each stage's failure short-circuits the
// rest.
//
// Mutation testing (`ergon check mutation`) is intentionally NOT
// part of the umbrella. `gremlins unleash` runs minutes per layer
// and is not suitable for a pre-merge gate; run it explicitly via
// the subcommand on a nightly cadence or before a release.
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run the full pre-merge gate",
	Long: "Runs the umbrella check sequence: mod verify, lint, test, " +
		"coverage, skip-expiry, error-prefix, and vuln. Each stage's " +
		"failure short-circuits the rest. Subcommands run individual " +
		"stages.\n\nMutation testing is excluded — it is slow and " +
		"belongs in a nightly job. Run `ergon check mutation` explicitly.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
		runner := xexec.Command{}

		root, mods, err := discover.Resolve(ctx, cfg.Modules)
		if err != nil {
			return err
		}
		importPath, err := discover.ImportPath(root)
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
		name := cfg.Name
		if name == "" {
			name = filepath.Base(root)
		}
		coverageDir := filepath.Join(root, "."+name, "coverage")
		if err = coverage.Run(ctx, runner, stdout, stderr,
			root, coverageDir, importPath+"/", cfg.Checks.Coverage,
			cfg.Checks.Excludes, cfg.Checks.Skips,
			coverage.RunOptions{}); err != nil {
			return err
		}
		goFiles, err := discover.GitFiles(ctx, runner, root, ".go")
		if err != nil {
			return err
		}
		if err = skipexpiry.Run(stdout, stderr, root, goFiles); err != nil {
			return err
		}
		if err = errorprefix.Run(stdout, stderr, root, goFiles, cfg.Checks.ErrorPrefix); err != nil {
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
