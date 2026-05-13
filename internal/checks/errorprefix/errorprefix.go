// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package errorprefix

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// packagePattern matches the Go `package <name>` declaration.
var packagePattern = regexp.MustCompile(`^package\s+(\w+)`)

// errorsNewPattern matches a call to `errors.New("...")` capturing
// the literal string. Only double-quoted strings are recognised;
// the codebase convention does not use backtick literals for
// sentinel messages.
var errorsNewPattern = regexp.MustCompile(`errors\.New\("([^"]+)"\)`)

// prunedDirs lists directory basenames the walker skips.
var prunedDirs = []string{".git", "vendor", "dist", "node_modules"}

// Run walks every directory in cfg.TargetDirs and reports every
// non-test `errors.New(...)` whose literal does not start with the
// file's package name (optionally followed by `.<sub>` for nested
// scopes) and a colon. Test files (`*_test.go`) are excluded —
// test errors are not sentinels.
func Run(stdout, stderr io.Writer, root string, cfg Config) error {
	cfg = withDefaults(cfg)
	var violations []finding
	var scanned int

	for _, dir := range cfg.TargetDirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat %s: %w", base, err)
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if slices.Contains(prunedDirs, d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			f, count := scanFile(path, string(body))
			scanned += count
			violations = append(violations, f...)
			return nil
		})
		if err != nil {
			return err
		}
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
