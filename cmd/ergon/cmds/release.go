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
	version    string
	dryRun     bool
	noTag      bool
	noBump     bool
	noPush     bool
	allowDirty bool
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
			releaseFlags.version,
			releaseFlags.dryRun,
			releaseFlags.noTag,
			releaseFlags.noBump,
			releaseFlags.noPush,
			releaseFlags.allowDirty,
		)
		if err != nil {
			return err
		}
		// release.Modules, when set, replaces the global module
		// list for `ergon release` only — other subcommands keep
		// using cfg.Modules. Empty falls through to the global
		// list, which itself falls back to `go.work` discovery.
		modScope := cfg.Modules
		if len(cfg.Release.Modules) > 0 {
			modScope = cfg.Release.Modules
		}
		root, mods, err := discover.Resolve(cmd.Context(), modScope)
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
	releaseCmd.Flags().StringVar(&releaseFlags.version, "version", "",
		"Pin every module to this version (vX.Y.Z) regardless of bump inference; useful for coordinated releases and resuming partial failures")
	releaseCmd.Flags().BoolVar(&releaseFlags.dryRun, "dry-run", false,
		"Print the plan; change nothing")
	releaseCmd.Flags().BoolVar(&releaseFlags.noTag, "no-tag", false,
		"Print the plan but do not create tags")
	releaseCmd.Flags().BoolVar(&releaseFlags.noBump, "no-bump", false,
		"Skip the intra-workspace go.mod dependency bump (no `chore(release):` commit produced)")
	releaseCmd.Flags().BoolVar(&releaseFlags.noPush, "no-push", false,
		"Keep tags and commits local; implies --no-bump (tidy needs published tags to resolve)")
	releaseCmd.Flags().BoolVar(&releaseFlags.allowDirty, "allow-dirty", false,
		"Permit uncommitted changes in the working tree (default: error out so the release commit stays clean)")
	rootCmd.AddCommand(releaseCmd)
}
