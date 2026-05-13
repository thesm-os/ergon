// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// Coverage converts every per-module `.out` profile under
// in.CoverageDir into a sibling HTML report via
// `go tool cover -html`. Modules whose profile does not exist
// (e.g., a module with no tests) are skipped with a stdout note.
//
// Requires `ergon test` to have run first to produce the profiles;
// this command does not run tests itself.
func Coverage(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs,
) error {
	if in.CoverageDir == "" {
		return fmt.Errorf("coverage dir not set")
	}
	return modules.Iterate(ctx, in.Modules, func(ctx context.Context, m modules.Module) error {
		profile := coverageFile(in.CoverageDir, m)
		if _, err := os.Stat(profile); err != nil {
			fmt.Fprintf(stdout, "[%s] no coverage profile (skipped)\n", m.Dir)
			return nil //nolint:nilerr // missing profile is intentional skip
		}
		html := strings.TrimSuffix(profile, ".out") + ".html"
		fmt.Fprintf(stdout, "[%s] %s -> %s\n", m.Dir, filepath.Base(profile), filepath.Base(html))
		return runner.Run(ctx,
			xexec.Options{Dir: in.Root, Stdout: stdout, Stderr: stderr},
			"go", "tool", "cover", "-html="+profile, "-o", html)
	})
}

// readFile is a thin wrapper around os.ReadFile that returns the
// file's contents as a string. Centralised so [scanFuzzInFile] can
// be swapped behind a seam in tests if the need arises.
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
