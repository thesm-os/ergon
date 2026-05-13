// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/license"
)

// licenseVerify drives the `--verify` flag on `ergon license`.
// When false, license.Apply rewrites headers in place; when true,
// license.Verify confirms every file's header is current without
// modifying anything.
var licenseVerify bool

// licenseCmd is `ergon license`. Apply mode is the default; pass
// `--verify` for the CI gate.
var licenseCmd = &cobra.Command{
	Use:   "license",
	Short: "Apply SPDX license headers (use --verify to check)",
	Long: "Walks the repository's Go sources and applies the header " +
		"declared in `.go-license.yml`. With --verify, exits non-zero " +
		"when any file's header is missing or stale.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := discover.Root(cmd.Context())
		if err != nil {
			return err
		}
		if licenseVerify {
			return license.Verify(cmd.Context(), xexec.Command{},
				cmd.OutOrStdout(), cmd.ErrOrStderr(), root, cfg.License)
		}
		return license.Apply(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), root, cfg.License)
	},
}

// init attaches licenseCmd to the root and registers --verify.
func init() {
	licenseCmd.Flags().BoolVar(&licenseVerify, "verify",
		false, "Check headers without modifying files")
	rootCmd.AddCommand(licenseCmd)
}
