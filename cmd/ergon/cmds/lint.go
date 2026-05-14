// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/lint"
	"go.thesmos.sh/ergon/internal/stage"
)

// lintOnly / lintSkip capture the per-invocation CLI overrides
// for `ergon lint`'s stage filter. They compose with the config
// values (`lint.enabled` / `lint.disabled` in `.ergon.yaml`) per
// the precedence rules on [stage.Filter]. Package-scope so the
// init() flag wiring can bind them; rebinding on every Execute
// run is fine because cobra resets them from the flag set.
var (
	lintOnly []string
	lintSkip []string
)

// lintCmd is `ergon lint`. Running it bare invokes [lint.All] with
// the stage filter composed from `.ergon.yaml`'s `lint.enabled` /
// `lint.disabled` plus the CLI `--only` / `--skip` flags;
// subcommand files attach per-stage commands to it.
var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Run the full lint suite",
	Long: "Runs go vet, golangci-lint, markdownlint-cli2 (reporting " +
		"only), and `go-license --verify` across the discovered " +
		"modules. Subcommands run individual stages.\n\n" +
		"The stage list can be narrowed via `.ergon.yaml`'s " +
		"`lint.enabled` / `lint.disabled` or per-invocation via " +
		"`--only` / `--skip`. Stage names are `vet`, `go`, `md`, " +
		"`license`.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return lint.All(
			cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			lint.Inputs{Root: root, Modules: mods},
			cfg.Markdown, cfg.License,
			stage.Filter{
				Enabled:  cfg.Lint.Enabled,
				Disabled: cfg.Lint.Disabled,
				Only:     lintOnly,
				Skip:     lintSkip,
			},
			stageOpts(),
		)
	},
}

// init attaches lintCmd to the root and binds the stage-filter
// flags. Per-stage subcommands attach from their own init()
// functions.
func init() {
	lintCmd.Flags().StringSliceVar(&lintOnly, "only", nil,
		"run only these lint stages (comma-separated; beats --skip and the config)")
	lintCmd.Flags().StringSliceVar(&lintSkip, "skip", nil,
		"skip these lint stages (comma-separated; unions with lint.disabled in config)")
	rootCmd.AddCommand(lintCmd)
}
