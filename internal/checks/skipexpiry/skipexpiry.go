// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package skipexpiry enforces the flaky-test policy's skip-expiry
// rule: every `t.Skip` whose message declares an `expires
// YYYY-MM-DD` date must be removed (or the skipped test fixed)
// before that date. An expired skip is a direct signal that a
// deferred fix was forgotten.
//
// Detection is AST-based — the scanner finds real `Skip(...)`
// method calls and reads the string literal argument. String
// literals embedded in other code or in comments do not register.
package skipexpiry

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// expiryPattern extracts the YYYY-MM-DD date from inside a skip
// message. The scanner has already isolated the message (it's the
// string literal handed to `t.Skip`), so the pattern looks only
// for the expires clause within it.
var expiryPattern = regexp.MustCompile(`expires\s+(\d{4}-\d{2}-\d{2})`)

// Today returns the current date in the local timezone formatted
// as YYYY-MM-DD. Exposed as a package-level variable so tests can
// pin it to a deterministic value.
var Today = func() string {
	return time.Now().Format("2006-01-02")
}

// Run scans every `_test.go` entry in files for `t.Skip(...)`
// calls whose message carries an `expires YYYY-MM-DD` clause and
// reports those whose date is on or before today. Returns nil
// when no expired skip is found; a non-nil error names the count
// of offenders.
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
		findings, count, err := scanFile(rel, body, today)
		if err != nil {
			return err
		}
		scanned += count
		expired = append(expired, findings...)
	}

	if len(expired) > 0 {
		for _, f := range expired {
			fmt.Fprintf(stderr, "EXPIRED: %s:%d — expires %s (today is %s)\n",
				f.Path, f.Line, f.Expiry, today)
		}
		return fmt.Errorf("skipexpiry: %d skip(s) past expiry", len(expired))
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

// scanFile parses body as a Go source file and returns every
// expired-skip call site. The AST walker tracks two patterns:
//
//   - `t.Skip("...expires YYYY-MM-DD...")` — the canonical form.
//     The receiver name is not constrained; any selector whose
//     method is `Skip` and whose first arg is a string literal
//     counts.
//   - `t.Skipf("...expires YYYY-MM-DD...", ...)` — same shape with
//     a formatting variant.
//
// Returns the parser error verbatim when body is not valid Go.
func scanFile(path string, body []byte, today string) ([]finding, int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, body, parser.SkipObjectResolution)
	if err != nil {
		return nil, 0, fmt.Errorf("parse %s: %w", path, err)
	}

	var out []finding
	var scanned int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "Skip" && sel.Sel.Name != "Skipf" {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		msg, ok := stringLiteral(call.Args[0])
		if !ok {
			return true
		}
		match := expiryPattern.FindStringSubmatch(msg)
		if match == nil {
			return true
		}
		scanned++
		expiry := match[1]
		if expiry < today {
			out = append(out, finding{
				Path:   path,
				Line:   fset.Position(call.Pos()).Line,
				Expiry: expiry,
			})
		}
		return true
	})
	return out, scanned, nil
}

// stringLiteral returns the unquoted contents of expr when it is
// a Go string literal, plus a boolean reporting whether the
// extraction succeeded.
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}
