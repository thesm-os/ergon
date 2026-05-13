// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package test

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// fuzzFuncRe matches the canonical `func FuzzXxx(` declaration in
// Go test sources. The capture group is the function name.
var fuzzFuncRe = regexp.MustCompile(`^func (Fuzz[A-Z]\w*)\(`)

// FuzzTarget identifies one fuzz target by module, package path
// (relative to the module root), and function name.
type FuzzTarget struct {
	Module modules.Module
	PkgRel string
	Name   string
}

// Fuzz discovers every Fuzz* target across the given modules and
// runs each sequentially for cfg.FuzzTime via
// `go test -run=^$ -fuzz=^Name$ -fuzztime=...`. Discovery is
// source-level (no compilation) so a syntactically valid test file
// is enough to register a target.
//
// First-failure short-circuits; the offending target is named in
// the wrapped error.
func Fuzz(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	in Inputs, cfg Config,
) error {
	targets, err := DiscoverFuzzTargets(in.Root, in.Modules)
	if err != nil {
		return fmt.Errorf("discover fuzz targets: %w", err)
	}
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "no fuzz targets found")
		return nil
	}
	for _, t := range targets {
		fmt.Fprintf(stdout, "[%s] fuzz %s in %s\n", t.Module.Dir, t.Name, t.PkgRel)
		err := runner.Run(
			ctx,
			optsFor(in.Root, t.Module, stdout, stderr),
			"go", "test",
			"-run=^$",
			"-fuzz=^"+t.Name+"$",
			"-fuzztime="+cfg.FuzzTime.String(),
			t.PkgRel,
		)
		if err != nil {
			return fmt.Errorf("[%s] fuzz %s: %w", t.Module.Dir, t.Name, err)
		}
	}
	return nil
}

// DiscoverFuzzTargets walks each module's tree looking for `_test.go`
// files containing top-level `func Fuzz*` declarations. Each
// discovered target produces one [FuzzTarget] in the returned slice.
//
// Directory pruning skips `.git`, `vendor`, and any path containing
// a `testdata` segment.
func DiscoverFuzzTargets(root string, mods []modules.Module) ([]FuzzTarget, error) {
	var out []FuzzTarget
	for _, m := range mods {
		modRoot := filepath.Join(root, m.Dir)
		err := filepath.WalkDir(modRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "vendor" || name == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			names, err := scanFuzzInFile(path)
			if err != nil {
				return fmt.Errorf("scan %s: %w", path, err)
			}
			if len(names) == 0 {
				return nil
			}
			pkgDir := filepath.Dir(path)
			rel, err := filepath.Rel(modRoot, pkgDir)
			if err != nil {
				return err
			}
			pkgRel := "./" + filepath.ToSlash(rel)
			if rel == "." {
				pkgRel = "./"
			}
			for _, name := range names {
				out = append(out, FuzzTarget{Module: m, PkgRel: pkgRel, Name: name})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scanFuzzInFile returns the names of every `func Fuzz*` top-level
// declaration in the given Go source file. Source is parsed line by
// line with [fuzzFuncRe]; this is intentionally cheaper than a full
// AST parse and is sufficient because Go style places the `func`
// keyword in column zero of every declaration.
func scanFuzzInFile(path string) ([]string, error) {
	body, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.Lines(body) {
		match := fuzzFuncRe.FindStringSubmatch(line)
		if match != nil {
			names = append(names, match[1])
		}
	}
	return names, nil
}
