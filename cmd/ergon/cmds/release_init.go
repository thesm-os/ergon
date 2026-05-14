// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/release"
)

// releaseInitFlags captures the raw cobra flag values for
// `ergon release init`. [resolveReleaseInitVars] normalises them
// into the [release.ScaffoldVars] [release.Scaffold] consumes.
var releaseInitFlags struct {
	name     string
	mains    []string
	upx      bool
	homebrew string
	docker   string
	force    bool
}

// releaseInitCmd is `ergon release init`. Writes a starter
// `.goreleaser.yml`, matching `.github/workflows/release.yml`,
// and per-module `internal/version/{version.go,version_test.go}`
// files into the current directory.
//
// Optional blocks (UPX binary packing, Homebrew cask publishing,
// Docker image publishing) activate when their corresponding
// flag is set. Existing files are skipped with a notice unless
// --force is passed; the command is safe to re-run on a
// partially-initialised repository to fill in only the gaps.
var releaseInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold goreleaser + release workflow",
	Long: "Writes a starter `.goreleaser.yml`, a matching " +
		"`.github/workflows/release.yml`, and per-module " +
		"`internal/version/version.go` (plus its test) into the " +
		"current directory.\n\nOptional sections activate via flags:\n\n" +
		"  --upx                 UPX binary packing (linux + windows_amd64 only)\n" +
		"  --homebrew owner/repo Homebrew tap cask publishing\n" +
		"  --docker <prefix>     Docker image publishing (multi-arch, one image per binary;\n" +
		"                        e.g. --docker ghcr.io/myorg → images at\n" +
		"                        ghcr.io/myorg/<binary> for each build)\n\n" +
		"`--main` (repeatable) enumerates the build targets; each " +
		"value is a path to a Go main package, e.g. `./cmd/foo` or " +
		"`./cli`. The scaffold walks each path upward looking for " +
		"the nearest enclosing `go.mod` so single-module, submodule, " +
		"and nested-submodule layouts all generate correctly. " +
		"Defaults to `./cmd/<name>` when no --main is passed.\n\n" +
		"Files that already exist are skipped with a notice unless " +
		"--force is set.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dest, err := os.Getwd()
		if err != nil {
			return err
		}
		vars, err := resolveReleaseInitVars(dest, releaseInitFlags.name,
			releaseInitFlags.mains, releaseInitFlags.upx,
			releaseInitFlags.homebrew, releaseInitFlags.docker)
		if err != nil {
			return err
		}
		return release.Scaffold(cmd.OutOrStdout(), dest, vars, releaseInitFlags.force)
	},
}

// resolveReleaseInitVars normalises the raw cobra flag values
// into a [release.ScaffoldVars]. Pure aside from the `go.mod`
// reads [release.ResolveBuildSpec] performs, so the resolution
// logic is independently testable.
//
// Defaults and validation:
//
//   - name: basename of dest when --name is empty.
//   - mains: `[./cmd/<name>]` when --main is empty; one
//     [release.BuildSpec] per entry, resolved via
//     [release.ResolveBuildSpec].
//   - --homebrew: when set, must parse as `owner/repo`; any other
//     shape is a usage error.
//   - --docker: passes the image reference through verbatim;
//     [release.ParseDockerRegistry] derives the registry for the
//     workflow's login step.
func resolveReleaseInitVars(
	dest, name string, mains []string, upx bool, homebrew, docker string,
) (release.ScaffoldVars, error) {
	if name == "" {
		name = filepath.Base(dest)
	}
	if len(mains) == 0 {
		mains = []string{"./cmd/" + name}
	}

	builds := make([]release.BuildSpec, 0, len(mains))
	for _, m := range mains {
		spec, err := release.ResolveBuildSpec(dest, m)
		if err != nil {
			return release.ScaffoldVars{}, err
		}
		builds = append(builds, spec)
	}

	vars := release.ScaffoldVars{
		Name:   name,
		Builds: builds,
		UPX:    upx,
	}

	if homebrew != "" {
		owner, repo, ok := splitOwnerRepo(homebrew)
		if !ok {
			return release.ScaffoldVars{}, fmt.Errorf(
				"--homebrew expects owner/repo, got %q", homebrew)
		}
		vars.Homebrew = true
		vars.HomebrewTapOwner = owner
		vars.HomebrewTapName = repo
	}
	if docker != "" {
		vars.Docker = true
		vars.DockerPrefix = docker
		vars.DockerRegistry = release.ParseDockerRegistry(docker)
	}
	return vars, nil
}

// splitOwnerRepo parses an `owner/repo` string into its two
// segments. Returns ok=false when the input is not exactly one
// `/`-separated pair of non-empty segments.
func splitOwnerRepo(s string) (owner, repo string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			if i == 0 || i == len(s)-1 {
				return "", "", false
			}
			rest := s[i+1:]
			for j := 0; j < len(rest); j++ {
				if rest[j] == '/' {
					return "", "", false
				}
			}
			return s[:i], rest, true
		}
	}
	return "", "", false
}

// init attaches releaseInitCmd to releaseCmd and registers its
// flags. The `ergon release init` surface is intentionally
// distinct from `ergon init` — that command scaffolds the
// per-repo dev surface (Makefile, .ergon.yaml, ...), this one
// scaffolds the release pipeline plus the version package the
// pipeline's ldflags target.
func init() {
	releaseInitCmd.Flags().StringVar(&releaseInitFlags.name, "name", "",
		"Project identifier (defaults to the basename of the CWD)")
	releaseInitCmd.Flags().StringSliceVar(&releaseInitFlags.mains, "main", nil,
		"Path to a Go main package to build; repeat for multi-binary releases (default: ./cmd/<name>)")
	releaseInitCmd.Flags().BoolVar(&releaseInitFlags.upx, "upx", false,
		"Enable UPX binary packing in goreleaser (linux + windows_amd64)")
	releaseInitCmd.Flags().StringVar(&releaseInitFlags.homebrew, "homebrew", "",
		"Enable Homebrew cask publishing to the given owner/repo tap")
	releaseInitCmd.Flags().StringVar(&releaseInitFlags.docker, "docker", "",
		"Enable Docker image publishing under the given registry prefix (e.g. ghcr.io/owner); each binary becomes <prefix>/<binary>")
	releaseInitCmd.Flags().BoolVar(&releaseInitFlags.force, "force", false,
		"Overwrite existing files (default: skip with a notice)")
	releaseCmd.AddCommand(releaseInitCmd)
}
