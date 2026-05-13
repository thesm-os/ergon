// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/scaffold"
)

// initFlags captures the raw cobra flag values for `ergon init`.
var initFlags struct {
	name   string
	module string
	force  bool
}

// initCmd is `ergon init`. Writes the starter file set (Makefile,
// .ergon.yaml, .gitignore, README.md, .github/workflows/ci.yml)
// into the current directory.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a new ergon-driven repository",
	Long: "Writes a Makefile, `.ergon.yaml`, `.gitignore`, README, and " +
		"a starter `.github/workflows/ci.yml` into the current " +
		"directory. Refuses to overwrite existing files unless --force " +
		"is passed.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		vars := scaffold.Vars{
			Name:   initFlags.name,
			Module: initFlags.module,
		}
		if vars.Name == "" {
			vars.Name = filepath.Base(cwd)
		}
		return scaffold.Run(cmd.OutOrStdout(), cwd, vars, initFlags.force)
	},
}

// init wires init's flags onto rootCmd. PersistentPreRunE is
// suppressed so the user can run `ergon init` outside an existing
// ergon repo (where `config.Load` would have nothing to parse).
func init() {
	initCmd.Flags().StringVar(&initFlags.name, "name", "",
		"Project identifier (defaults to the basename of the CWD)")
	initCmd.Flags().StringVar(&initFlags.module, "module", "",
		"Go module path (reserved for future templates)")
	initCmd.Flags().BoolVar(&initFlags.force, "force", false,
		"Overwrite existing files")
	rootCmd.AddCommand(initCmd)
}
