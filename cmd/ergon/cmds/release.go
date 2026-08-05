// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/commitmsg"
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

// releasePreflight rejects a release whose generated pin-commit
// message would violate the repository's own commit conventions.
//
// The pipeline commits with --no-verify, because a hook running
// the workspace-wide gate cannot pass at an interior layer (see
// [release.PinCommitMessage]). That trades the commit-msg hook's
// convention check for this one, moved to release entry: a repo
// whose `checks.commit_msg.types` omits `chore` is told so before
// any tag is created, rather than discovering a non-conforming
// commit in history afterwards.
//
// Modes that never produce a pin commit — --dry-run, --no-tag,
// --no-bump — skip the check; refusing them would block a
// legitimate tag-only release over a commit that is never made.
func releasePreflight(opts release.Options, mcfg commitmsg.Config) error {
	if opts.DryRun || opts.NoTag || opts.NoBump {
		return nil
	}
	// One representative tag: the subject is constant and every
	// body line has the same shape, so a single entry exercises
	// every rule the validator applies.
	msg := release.PinCommitMessage([]string{"example/v0.0.0"})
	if err := commitmsg.Validate(msg, mcfg); err != nil {
		return fmt.Errorf(
			"release: the pin commit this release would create violates "+
				"checks.commit_msg in .ergon.yaml: %w\n"+
				"  the release pipeline commits with --no-verify, so this is "+
				"checked here instead of by the commit-msg hook\n"+
				"  allow `chore` in checks.commit_msg.types, or pass "+
				"--no-bump to tag without rewriting go.mod files",
			err)
	}
	return nil
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
		if err = releasePreflight(opts, cfg.Checks.CommitMsg); err != nil {
			return err
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
