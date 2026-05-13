// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package discover

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// GitFiles returns every file git would touch — every file that
// is either tracked (cached) or untracked-but-not-ignored. Files
// listed in `.gitignore` and `.git/info/exclude` are excluded
// automatically because `git ls-files --exclude-standard` honours
// those sources.
//
// The slash separator in returned paths is the one git uses
// (forward slash on every platform); paths are repository-relative.
//
// Suffix, when non-empty, restricts the result to files whose name
// ends with that string (e.g. `.go`). Pass `""` for every file.
func GitFiles(
	ctx context.Context, runner xexec.Runner, root, suffix string,
) ([]string, error) {
	var buf bytes.Buffer
	err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &buf, Stderr: &buf},
		"git", "ls-files",
		"--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w: %s", err, strings.TrimSpace(buf.String()))
	}

	var out []string
	for name := range bytes.SplitSeq(buf.Bytes(), []byte{0}) {
		if len(name) == 0 {
			continue
		}
		s := string(name)
		if suffix != "" && !strings.HasSuffix(s, suffix) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}
