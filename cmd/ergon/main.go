// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Command ergon is a CLI that runs lifecycle tasks for Go projects:
// format, lint, test, benchmark, release. See `ergon help` for the
// subcommand surface.
package main

import (
	"os"

	"go.thesmos.sh/ergon/cmd/ergon/cmds"
)

func main() {
	if err := cmds.Execute(); err != nil {
		os.Exit(1)
	}
}
