// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"context"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/lint"
	"go.thesmos.sh/ergon/internal/stage"
)

// lintOnly / lintSkip capture the per-invocation CLI overrides
// for `ergon lint`'s stage filter. They compose with the config
// values (`lint.enabled` / `lint.disabled` in `.ergon.yaml`) per
// the precedence rules on [stage.Filter].
var (
	lintOnly []string
	lintSkip []string
)

// lintCmd is `ergon lint`. Running it bare invokes [lint.All] with
// every static-analysis stage in scope (`vet`, `go`, `md`,
// `license`, `skip-expiry`, `error-prefix`, `vuln`); per-stage
// subcommand files attach the individual entry points to it.
//
// The stage filter is composed from `.ergon.yaml`'s `lint.enabled`
// / `lint.disabled` plus the CLI `--only` / `--skip` flags. CLI
// `--only` wins absolutely; `--skip` unions with the config
// denylist. An unknown stage name surfaces as a usage error
// before any work runs.
var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Run the full lint suite",
	Long: "Runs go vet, golangci-lint, markdownlint-cli2 (reporting " +
		"only), `go-license --verify`, skip-expiry, error-prefix, " +
		"and govulncheck across the discovered modules. Subcommands " +
		"run individual stages.\n\nThe stage list can be narrowed " +
		"via `.ergon.yaml`'s `lint.enabled` / `lint.disabled` or " +
		"per-invocation via `--only` / `--skip`. Stage names are " +
		"`vet`, `go`, `md`, `license`, `skip-expiry`, `error-prefix`, " +
		"`vuln`.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		runner := xexec.Command{}
		root, mods, err := discover.Resolve(ctx, cfg.Modules)
		if err != nil {
			return err
		}
		return lint.All(
			ctx, runner,
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			lint.Inputs{Root: root, Modules: mods, GitFiles: gitFilesFor(ctx, runner, root)},
			cfg.Markdown, cfg.License, cfg.Lint.ErrorPrefix,
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

// gitFilesFor returns a memoising lazy resolver for repo-tracked
// `.go` files. The skip-expiry and error-prefix stages each call
// it once when they run; the cached slice means a single
// `git ls-files` invocation covers both stages. Used by every
// caller of [lint.All] (the umbrella and its standalone-stage
// commands) so the discovery cost is paid once per run.
func gitFilesFor(ctx context.Context, runner xexec.Runner, root string) func() ([]string, error) {
	var (
		cached []string
		err    error
		done   bool
	)
	return func() ([]string, error) {
		if done {
			return cached, err
		}
		cached, err = discover.GitFiles(ctx, runner, root, ".go")
		done = true
		return cached, err
	}
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
