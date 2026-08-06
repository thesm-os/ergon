// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package mutation

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// nestedModules returns the directories beneath walkRoot that hold
// their own go.mod, expressed relative to modRoot in slash form.
//
// This is the boundary gremlins does not know about. It is handed a
// path and walks the filesystem beneath it, so a layer declared at a
// repository root mutates every sibling module in the workspace —
// and any module merely present on disk. Their tests never run, so
// every one of those mutants is counted "not covered" and the
// layer's mutator coverage becomes a statement about the workspace
// rather than about the module.
//
// Measured on go.thesmos.sh/testkit, whose root module sits above
// four workspace modules and one non-workspace tree: the root layer
// produced 4544 mutants of which 4057 belonged to other modules,
// reporting 10.6% mutator coverage against 100% line coverage.
// Excluding the nested modules gives 98.97% over an identical 482
// runnable mutants — nothing real was removed.
//
// The rule is "stop at any nested go.mod", not "stop at modules
// go.work lists". Those coincide for a workspace member but only the
// former also covers a module on disk that the workspace omits, and
// it needs no go.work to state. It is exactly `go list ./...`: a
// module's package set ends where a nested module begins, which is
// why the coverage gate — which resolves through the module graph —
// never had this defect.
//
// Directories Go itself ignores when resolving `./...` are skipped
// rather than descended: names beginning with `.` or `_`, and
// `vendor`. A module underneath one of those is invisible to the
// build, so it cannot be a boundary the build respects.
func nestedModules(modRoot, walkRoot string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(walkRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() || p == walkRoot {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "vendor" {
			return fs.SkipDir
		}
		if _, statErr := os.Stat(filepath.Join(p, "go.mod")); statErr == nil {
			rel, relErr := filepath.Rel(modRoot, p)
			if relErr != nil {
				return relErr
			}
			out = append(out, filepath.ToSlash(rel))
			// Not descended into: a module nested inside a nested
			// module is already excluded by its parent's prefix, and
			// recording it would report the same files twice.
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf(
			"mutation: scan for nested modules under %s: %w", walkRoot, err)
	}
	sort.Strings(out)
	return out, nil
}

// withNestedExclusions folds the nested-module directories into the
// policy-derived `--exclude-files` regex.
//
// gremlins matches the pattern against paths relative to its working
// directory, which [resolveInvocation] sets to the module root, so
// the anchors are `^<dir>/`. Verified against gremlins directly: one
// alternation behaves identically to repeated -E flags, and composes
// with an existing policy pattern.
//
// The two halves are parenthesised because either may itself be an
// alternation, and `a|b|c|d` would otherwise let a policy branch bind
// across the join.
func withNestedExclusions(base string, nested []string) string {
	if len(nested) == 0 {
		return base
	}
	parts := make([]string, 0, len(nested))
	for _, n := range nested {
		parts = append(parts, "^"+regexp.QuoteMeta(n)+"/")
	}
	joined := strings.Join(parts, "|")
	if base == "" {
		return joined
	}
	return "(" + base + ")|(" + joined + ")"
}
