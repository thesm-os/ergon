// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/license"
)

// lintLicenseCmd is `ergon lint license`. Runs `go-license --verify`
// against the repository's Go sources — the read-only counterpart
// to `ergon license`.
var lintLicenseCmd = &cobra.Command{
	Use:   "license",
	Short: "Verify SPDX license headers",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := discover.Root(cmd.Context())
		if err != nil {
			return err
		}
		return license.Verify(cmd.Context(), xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(), root, cfg.License)
	},
}

func init() {
	lintCmd.AddCommand(lintLicenseCmd)
}
