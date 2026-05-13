// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/discover"
	"go.thesmos.sh/ergon/internal/doctor"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// doctorCmd is `ergon doctor`. Probes the local environment for
// every binary ergon shells out to and compares the installed Go
// toolchain against the `go` directive in `go.mod`. Exits
// non-zero when at least one required binary is missing so CI
// can gate on a clean doctor report.
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Probe the local environment for required tools",
	Long: "Verifies every binary `ergon bootstrap` installs is on " +
		"PATH (gofumpt, gci, golangci-lint, govulncheck, go-license, " +
		"benchstat, gremlins, plus any `bootstrap.extra_tools`), " +
		"probes for markdownlint-cli2, and compares the installed Go " +
		"toolchain against the version declared in `go.mod`. Exits " +
		"non-zero when at least one required binary is missing.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		root, err := discover.Root(ctx)
		if err != nil {
			return err
		}
		return doctor.Run(ctx, xexec.Command{},
			cmd.OutOrStdout(), cmd.ErrOrStderr(),
			root, cfg.Bootstrap)
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
