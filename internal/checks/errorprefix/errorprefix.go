// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package errorprefix

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// packagePattern matches the Go `package <name>` declaration.
var packagePattern = regexp.MustCompile(`^package\s+(\w+)`)

// errorsNewPattern matches a call to `errors.New("...")` capturing
// the literal string. Only double-quoted strings are recognised;
// the codebase convention does not use backtick literals for
// sentinel messages.
var errorsNewPattern = regexp.MustCompile(`errors\.New\("([^"]+)"\)`)

// Run scans every non-test `.go` file in files for `errors.New`
// literals and reports those whose prefix does not match the
// file's package name (optionally `<pkg>.<sub>:` for nested
// scopes). files is a slice of repo-relative paths; root is the
// absolute repository root used to resolve each file on disk.
//
// cfg.TargetDirs narrows the scan to repo-relative prefix(es) —
// useful when the rule applies only to the library layers, not
// to cmd/.
func Run(stdout, stderr io.Writer, root string, files []string, cfg Config) error {
	cfg = withDefaults(cfg)
	var violations []finding
	var scanned int

	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if !underTargets(rel, cfg.TargetDirs) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		f, count := scanFile(rel, string(body))
		scanned += count
		violations = append(violations, f...)
	}

	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintf(stderr, "%s:%d: errors.New(%q) — expected prefix %q\n",
				v.Path, v.Line, v.Literal, v.Pkg+": ")
		}
		return fmt.Errorf("%d errors.New call(s) with wrong package prefix", len(violations))
	}
	if scanned == 0 {
		fmt.Fprintln(stdout, "no errors.New calls found")
	} else {
		fmt.Fprintf(stdout, "%d errors.New call(s) scanned, 0 violations\n", scanned)
	}
	return nil
}

// withDefaults fills any zero-value field on cfg from [Defaults].
func withDefaults(cfg Config) Config {
	d := Defaults()
	if len(cfg.TargetDirs) == 0 {
		cfg.TargetDirs = d.TargetDirs
	}
	return cfg
}

// underTargets reports whether rel sits under any directory in
// targets. A target of `.` matches every path.
func underTargets(rel string, targets []string) bool {
	for _, t := range targets {
		if t == "." || t == "" {
			return true
		}
		t = strings.TrimSuffix(t, "/")
		if rel == t || strings.HasPrefix(rel, t+"/") {
			return true
		}
	}
	return false
}

// finding records one prefix violation for reporting.
type finding struct {
	Path    string
	Line    int
	Literal string
	Pkg     string
}

// scanFile returns every prefix violation in body plus the total
// number of `errors.New` calls inspected. Lines are 1-indexed.
// Files that do not declare a package (or declare an empty name)
// produce no findings.
func scanFile(path, body string) ([]finding, int) {
	pkg := packageOf(body)
	if pkg == "" {
		return nil, 0
	}
	var out []finding
	var scanned int
	lineNo := 0
	for line := range strings.SplitSeq(body, "\n") {
		lineNo++
		match := errorsNewPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		scanned++
		literal := match[1]
		if !hasPrefix(literal, pkg) {
			out = append(out, finding{Path: path, Line: lineNo, Literal: literal, Pkg: pkg})
		}
	}
	return out, scanned
}

// packageOf extracts the first `package <name>` declaration in
// body. Returns the empty string when the source does not contain
// a recognisable declaration.
func packageOf(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		match := packagePattern.FindStringSubmatch(line)
		if match != nil {
			return match[1]
		}
	}
	return ""
}

// hasPrefix reports whether literal starts with an acceptable
// package prefix: `<pkg>:` or `<pkg>.` (for sub-package qualifiers
// like `kernel.patch:`).
func hasPrefix(literal, pkg string) bool {
	return strings.HasPrefix(literal, pkg+":") || strings.HasPrefix(literal, pkg+".")
}
