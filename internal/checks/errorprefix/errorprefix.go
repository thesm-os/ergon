// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package errorprefix

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Run scans every non-test `.go` file in files for `errors.New`
// call sites and reports those whose literal does not start with
// the file's package name (optionally `<pkg>.<sub>:` for nested
// scopes). Detection is AST-based — literal `errors.New("...")`
// strings inside comments or other code do not register.
//
// files is a slice of repo-relative paths; root is the absolute
// repository root used to resolve each file on disk.
//
// cfg.TargetDirs narrows the scan to repo-relative prefix(es) —
// useful when the rule applies only to the library layers, not
// to cmd/.
func Run(stdout, stderr io.Writer, root string, files []string, cfg Config) error {
	cfg = withDefaults(cfg)
	var violations []finding
	var scanned, exempted int

	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if !underTargets(rel, cfg.TargetDirs) {
			continue
		}
		// Counted only for files that were in scope, so the figure
		// reports what the exemption actually bought rather than
		// every file the targets already omitted.
		if excluded(rel, cfg) {
			exempted++
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return fmt.Errorf("errorprefix: read %s: %w", rel, err)
		}
		f, count, err := scanFile(rel, body)
		if err != nil {
			return err
		}
		scanned += count
		violations = append(violations, f...)
	}

	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintf(stderr, "%s:%d: errors.New(%q) — expected prefix %q\n",
				v.Path, v.Line, v.Literal, v.Pkg+": ")
		}
		return fmt.Errorf("errorprefix: %d errors.New call(s) with wrong package prefix", len(violations))
	}
	// The exemption is reported, never merely applied. An
	// exclusion nobody can see is how a lint quietly shrinks until
	// it covers nothing — `checks.excludes` carries a Reason field
	// that surfaces nowhere, and this list does not repeat that.
	suffix := ""
	if exempted > 0 {
		suffix = fmt.Sprintf(", %d file(s) excluded", exempted)
	}
	if scanned == 0 {
		fmt.Fprintf(stdout, "no errors.New calls found%s\n", suffix)
	} else {
		fmt.Fprintf(stdout, "%d errors.New call(s) scanned, 0 violations%s\n", scanned, suffix)
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

// scanFile parses body as a Go source file and returns every
// `errors.New("...")` call site whose literal does not start with
// the file's package name. Files that fail to parse surface the
// parser error verbatim.
func scanFile(path string, body []byte) ([]finding, int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, body, parser.SkipObjectResolution)
	if err != nil {
		return nil, 0, fmt.Errorf("errorprefix: parse %s: %w", path, err)
	}
	pkg := f.Name.Name
	if pkg == "" {
		return nil, 0, nil
	}

	var out []finding
	var scanned int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isErrorsNew(call.Fun) {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		msg, ok := stringLiteral(call.Args[0])
		if !ok {
			return true
		}
		scanned++
		if !hasPrefix(msg, pkg) {
			out = append(out, finding{
				Path:    path,
				Line:    fset.Position(call.Pos()).Line,
				Literal: msg,
				Pkg:     pkg,
			})
		}
		return true
	})
	return out, scanned, nil
}

// isErrorsNew reports whether expr resolves to a call of
// `errors.New`. The check is syntactic — any selector with
// `errors` as the package qualifier and `New` as the method name
// counts. Aliased imports (`stderrors "errors"`) are not
// recognised; the repository convention is the canonical name.
func isErrorsNew(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "errors" && sel.Sel.Name == "New"
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

// hasPrefix reports whether literal starts with an acceptable
// package prefix: `<pkg>:` or `<pkg>.` (for sub-package qualifiers
// like `kernel.patch:`).
func hasPrefix(literal, pkg string) bool {
	return strings.HasPrefix(literal, pkg+":") || strings.HasPrefix(literal, pkg+".")
}
