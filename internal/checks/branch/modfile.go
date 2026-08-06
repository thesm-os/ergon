// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package branch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"go.thesmos.sh/ergon/internal/modules"
)

// stagedModfile is an alternate go.mod written outside the source
// tree for one workspace module, plus the flag value gobco needs to
// use it.
type stagedModfile struct {
	// Path is the absolute path of the staged `.mod` file.
	Path string

	// Siblings names the workspace modules the staged file pins to
	// absolute directories. Empty means the module needed no
	// staging.
	Siblings []string
}

// stageModfile writes an alternate go.mod for the module at
// moduleDir that resolves its intra-workspace dependencies without
// the workspace.
//
// # Why this exists
//
// gobco instruments by copying the enclosing module to a temp
// directory and running `go test` there. Two things do not survive
// that move:
//
//   - `go.work`, which is a property of where a directory sits in
//     the tree. The relocated copy is not a workspace member, so
//     sibling modules stop resolving.
//   - a relative `replace` target such as `../alpha`, which now
//     points at a sibling of the temp directory.
//
// An ABSOLUTE replace target survives untouched, which is the whole
// fix. The staged file carries, for every sibling module this one
// imports, a `require` (so the module joins the build list at all)
// and a `replace` pointing at the sibling's real absolute directory.
// gobco receives it via `-test -modfile=<path>`; Go reads it in
// place of go.mod, and because the path is absolute it keeps
// resolving from the temp directory.
//
// Nothing in the source tree is touched: the staged file lives in
// the caller's temp directory and the real go.mod is never
// rewritten. A `.sum` sibling is written when the module has one,
// since Go derives the sum path from the modfile name and a cold
// module cache would otherwise fail to verify external deps.
//
// Returns a zero [stagedModfile] when the module imports no sibling
// workspace module — the common single-module case, where the real
// go.mod is already correct and gobco should be invoked without
// -modfile.
func stageModfile(
	root, moduleDir string, siblings []string,
	imports []modules.Import, tmpDir string,
) (stagedModfile, error) {
	if len(siblings) == 0 {
		return stagedModfile{}, nil
	}

	goModPath := filepath.Join(root, moduleDir, "go.mod")
	body, err := os.ReadFile(goModPath)
	if err != nil {
		return stagedModfile{}, fmt.Errorf("branch: read %s: %w", goModPath, err)
	}
	f, err := modfile.Parse(goModPath, body, nil)
	if err != nil {
		return stagedModfile{}, fmt.Errorf("branch: parse %s: %w", goModPath, err)
	}

	dirByPath := map[string]string{}
	for _, ip := range imports {
		dirByPath[ip.ImportPath] = ip.Dir
	}

	for _, sib := range siblings {
		dir, ok := dirByPath[sib]
		if !ok {
			continue
		}
		abs, absErr := filepath.Abs(filepath.Join(root, dir))
		if absErr != nil {
			return stagedModfile{}, fmt.Errorf("branch: resolve %s: %w", dir, absErr)
		}
		// A `replace` is inert without a `require` — the module has
		// to be in the build list for the replacement to apply. Any
		// existing requirement is left alone; its version is
		// irrelevant once a directory replacement is in force.
		if !requires(f, sib) {
			if reqErr := f.AddRequire(sib, initialRequireVersion(sib)); reqErr != nil {
				return stagedModfile{}, fmt.Errorf("branch: require %s: %w", sib, reqErr)
			}
		}
		if repErr := f.AddReplace(sib, "", abs, ""); repErr != nil {
			return stagedModfile{}, fmt.Errorf("branch: replace %s: %w", sib, repErr)
		}
	}

	f.SortBlocks()
	f.Cleanup()
	out, err := f.Format()
	if err != nil {
		return stagedModfile{}, fmt.Errorf("branch: format staged go.mod: %w", err)
	}

	// Both writes land inside tmpDir, which the caller created with
	// os.MkdirTemp; moduleSlug reduces moduleDir to one
	// [A-Za-z0-9_-] component, so neither a separator nor a `..`
	// element can reach the join. gosec's taint analysis cannot see
	// past the parameter.
	staged := filepath.Join(tmpDir, moduleSlug(moduleDir)+".mod")
	//nolint:gosec // G703: tmpDir is caller-created; the slug is separator-free
	if err := os.WriteFile(staged, out, 0o600); err != nil {
		return stagedModfile{}, fmt.Errorf("branch: write %s: %w", staged, err)
	}

	// Go derives the sum path by swapping the modfile's extension.
	// Copying the real go.sum keeps external dependencies verifiable
	// on a cold module cache.
	if sum, sumErr := os.ReadFile(filepath.Join(root, moduleDir, "go.sum")); sumErr == nil {
		sumPath := strings.TrimSuffix(staged, ".mod") + ".sum"
		//nolint:gosec // G703: derived from `staged`, same reasoning
		if err := os.WriteFile(sumPath, sum, 0o600); err != nil {
			return stagedModfile{}, fmt.Errorf("branch: write %s: %w", sumPath, err)
		}
	}

	return stagedModfile{Path: staged, Siblings: siblings}, nil
}

// moduleSlug turns a module directory into a single flat filename
// component. Every byte outside [A-Za-z0-9_-] is replaced, so the
// result cannot contain a separator or a `..` element and the staged
// file provably lands inside the caller's temp directory.
func moduleSlug(moduleDir string) string {
	var b strings.Builder
	for i := range len(moduleDir) {
		if c := moduleDir[i]; isSlugByte(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	if slug := b.String(); slug != "" && strings.Trim(slug, "_") != "" {
		return slug
	}
	return "root"
}

// isSlugByte reports whether c may appear verbatim in a staged
// filename. Written as one boolean expression rather than a
// multi-expression switch case: Go's coverage records a block
// starting after a case's expression list, so conditions written
// there sit outside every covered block and read as unreachable to
// mutation testing no matter how thoroughly they are exercised.
func isSlugByte(c byte) bool {
	return c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' ||
		c == '-'
}

// requires reports whether f already carries a requirement on path.
func requires(f *modfile.File, path string) bool {
	for _, r := range f.Require {
		if r != nil && r.Mod.Path == path {
			return true
		}
	}
	return false
}

// initialRequireVersion returns the version to require a sibling at
// when the module does not already require it. The value is never
// resolved — a directory replacement supersedes it — but it must be
// consistent with the path's major-version suffix or the go command
// rejects the file.
func initialRequireVersion(modPath string) string {
	if i := strings.LastIndex(modPath, "/v"); i >= 0 {
		if major := modPath[i+2:]; major != "" && isAllDigits(major) && major != "0" && major != "1" {
			return "v" + major + ".0.0"
		}
	}
	return "v0.0.0"
}

// isAllDigits reports whether s is a non-empty run of ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// siblingsFor returns every distinct workspace module, other than
// the package's own, that the packages in moduleDir import. It is
// the module-level union of [coupledModules] across a module's
// packages, so one staged modfile serves every package in it.
//
// The union is over [coupledModules], not [coupledModule]: a single
// package may import several siblings, and every one of them needs
// its relative `replace` rewritten. Taking one per package left the
// others relative and gobco's relocated copy failed to resolve
// them.
func siblingsFor(moduleDir string, pkgs []pkgInfo, imports []modules.Import) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range pkgs {
		if owningModuleDir(p.RepoRel, imports) != moduleDir {
			continue
		}
		for _, dep := range coupledModules(p, imports) {
			if _, dup := seen[dep]; dup {
				continue
			}
			seen[dep] = struct{}{}
			out = append(out, dep)
		}
	}
	return out
}
