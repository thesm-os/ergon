// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/release"
)

// releaseFlags captures the raw cobra flag values for
// `ergon release`. [release.NewOptions] resolves them into the
// [release.Options] the package consumes.
var releaseFlags struct {
	message    string
	forceMajor bool
	forceMinor bool
	forcePatch bool
	bumps      []string
	dryRun     bool
	noTag      bool
}

// releaseCmd is `ergon release`. Bumps module versions across the
// repository and creates annotated tags following the Go
// multi-module convention.
var releaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Multi-module tag bump",
	Long: "Bumps module versions across the discovered modules and " +
		"creates annotated git tags (root → `v1.2.3`, submodule " +
		"`foo/bar` → `foo/bar/v1.2.3`). Bump level resolves through " +
		"`--bump MODULE=LEVEL` overrides, `--major`/`--minor`/`--patch` " +
		"force flags, and conventional-commit inference scoped to each " +
		"module's history since its last tag.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		opts, err := release.NewOptions(
			releaseFlags.message,
			releaseFlags.forceMajor,
			releaseFlags.forceMinor,
			releaseFlags.forcePatch,
			releaseFlags.bumps,
			releaseFlags.dryRun,
			releaseFlags.noTag,
		)
		if err != nil {
			return err
		}
		root, mods, err := discover.Resolve(cmd.Context(), cfg.Modules)
		if err != nil {
			return err
		}
		return release.Run(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), root, mods, opts)
	},
}

// init attaches releaseCmd to the root and registers its flags.
func init() {
	releaseCmd.Flags().StringVarP(&releaseFlags.message, "message", "m", "",
		"Annotated-tag message (required unless --dry-run / --no-tag)")
	releaseCmd.Flags().BoolVar(&releaseFlags.forceMajor, "major", false,
		"Force major bump for every module")
	releaseCmd.Flags().BoolVar(&releaseFlags.forceMinor, "minor", false,
		"Force minor bump for every module")
	releaseCmd.Flags().BoolVar(&releaseFlags.forcePatch, "patch", false,
		"Force patch bump for every module")
	releaseCmd.Flags().StringSliceVar(&releaseFlags.bumps, "bump", nil,
		"Per-module bump override (MODULE=LEVEL; repeatable)")
	releaseCmd.Flags().BoolVar(&releaseFlags.dryRun, "dry-run", false,
		"Print the plan; change nothing")
	releaseCmd.Flags().BoolVar(&releaseFlags.noTag, "no-tag", false,
		"Print the plan but do not create tags")
	rootCmd.AddCommand(releaseCmd)
}
