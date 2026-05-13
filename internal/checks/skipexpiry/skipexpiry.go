// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package skipexpiry enforces the flaky-test policy's skip-expiry
// rule: every `t.Skip` whose message declares an `expires
// YYYY-MM-DD` date must be removed (or the skipped test fixed)
// before that date. An expired skip is a direct signal that a
// deferred fix was forgotten.
//
// The package walks the working tree for Go test files, parses
// every matching `t.Skip` line for the embedded date, and reports
// any whose date is on or before today's date in the local
// timezone.
package skipexpiry

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// expiryPattern matches `t.Skip(...)` lines that carry an
// `expires YYYY-MM-DD` clause. The capture group is the date in
// the canonical Go reference format. `.` is used (not `[^)]`) so
// the regex tolerates parenthesised content in the message
// (`TODO(owner): reason — expires …`).
var expiryPattern = regexp.MustCompile(`t\.Skip\(.*expires\s+(\d{4}-\d{2}-\d{2})`)

// Today returns the current date in the local timezone formatted
// as YYYY-MM-DD. Exposed as a package-level variable so tests can
// pin it to a deterministic value.
var Today = func() string {
	return time.Now().Format("2006-01-02")
}

// Run walks the tree under root for Go test files (`*_test.go`)
// and reports every `t.Skip("...expires YYYY-MM-DD")` whose date
// is on or before today. Returns nil when no expired skip is
// found; a non-nil error names every offender.
func Run(stdout, stderr io.Writer, root string) error {
	today := Today()
	var expired []finding
	var scanned int

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if slices.Contains(prunedDirs, name) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		findings, count := scanFile(path, string(body), today)
		scanned += count
		expired = append(expired, findings...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}

	if len(expired) > 0 {
		for _, f := range expired {
			fmt.Fprintf(stderr, "EXPIRED: %s:%d — expires %s (today is %s)\n",
				f.Path, f.Line, f.Expiry, today)
		}
		return fmt.Errorf("%d skip(s) past expiry", len(expired))
	}
	if scanned == 0 {
		fmt.Fprintln(stdout, "no t.Skip calls with expiry dates found")
	} else {
		fmt.Fprintf(stdout, "%d skip(s) scanned, 0 expired\n", scanned)
	}
	return nil
}

// prunedDirs lists directory basenames the walker skips. Same
// rule as the other checks: stay out of VCS internals, vendored
// dependencies, and build artefacts.
var prunedDirs = []string{".git", "vendor", "dist", "node_modules"}

// finding records one expired skip for reporting.
type finding struct {
	Path   string
	Line   int
	Expiry string
}

// scanFile returns every expired-skip finding from body plus the
// total count of dated skips inspected (used for the summary
// line). Lines are 1-indexed.
func scanFile(path, body, today string) ([]finding, int) {
	var out []finding
	var scanned int
	for i, line := range strings.Split(body, "\n") {
		match := expiryPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		scanned++
		expiry := match[1]
		if expiry < today {
			out = append(out, finding{
				Path:   path,
				Line:   i + 1,
				Expiry: expiry,
			})
		}
	}
	return out, scanned
}
