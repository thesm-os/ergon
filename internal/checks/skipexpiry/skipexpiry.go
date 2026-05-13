// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package skipexpiry enforces the flaky-test policy's skip-expiry
// rule: every `t.Skip` whose message declares an `expires
// YYYY-MM-DD` date must be removed (or the skipped test fixed)
// before that date. An expired skip is a direct signal that a
// deferred fix was forgotten.
//
// The package scans every supplied `_test.go` file (caller is
// responsible for the file list — typically
// [go.thesmos.sh/ergon/internal/discover.GitFiles] so gitignored
// paths drop out) for the expires clause and reports any whose
// date is on or before today's date in the local timezone.
package skipexpiry

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

// Run scans every `_test.go` entry in files for `t.Skip("...expires
// YYYY-MM-DD")` declarations and reports those whose date is on or
// before today. Returns nil when no expired skip is found; a
// non-nil error names the count of offenders.
//
// files is a slice of repo-relative paths; root is the absolute
// repository root used to resolve each file on disk.
func Run(stdout, stderr io.Writer, root string, files []string) error {
	today := Today()
	var expired []finding
	var scanned int

	for _, rel := range files {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		findings, count := scanFile(rel, string(body), today)
		scanned += count
		expired = append(expired, findings...)
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
	lineNo := 0
	for line := range strings.SplitSeq(body, "\n") {
		lineNo++
		match := expiryPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		scanned++
		expiry := match[1]
		if expiry < today {
			out = append(out, finding{
				Path:   path,
				Line:   lineNo,
				Expiry: expiry,
			})
		}
	}
	return out, scanned
}
