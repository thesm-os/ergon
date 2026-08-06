// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/clean"
	"go.thesmos.sh/ergon/internal/discover"
	"go.thesmos.sh/ergon/internal/runtmp"
)

// cleanCmd is `ergon clean`. Removes the build and coverage
// artefact directories ergon's other commands write
// (`bin/`, `.<name>/`, `dist/`).
var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove build and coverage artefacts",
	Long: "Removes `bin/`, `.<name>/` (e.g. `.ergon/`), and `dist/` " +
		"under the repository root. Does not touch the Go module " +
		"cache or test cache — `go clean` owns those.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := discover.Root(cmd.Context())
		if err != nil {
			return err
		}
		if err := clean.Run(cmd.OutOrStdout(), root); err != nil {
			return err
		}
		// Reclaim per-run temp roots abandoned by killed runs. The
		// deferred cleanup in Execute covers a normal exit and
		// SIGINT; a SIGKILL leaves the root behind, and on a machine
		// that interrupts checks often they accumulate in a
		// RAM-backed /tmp. runTmp is this invocation's own root and
		// is excluded — clean runs inside one.
		swept, sweepErr := runtmp.Sweep(filepath.Dir(runTmp), runTmp, runtmp.StaleAfter)
		if sweepErr != nil {
			return sweepErr
		}
		if swept > 0 {
			fmt.Fprintf(cmd.OutOrStdout(),
				"removed %d abandoned temp root(s) older than %s\n",
				swept, runtmp.StaleAfter)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cleanCmd)
}
