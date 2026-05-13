// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package discover resolves the repository root and the list of
// Go modules an ergon invocation operates on. The two functions
// — [Root] and [Modules] — exist as a unit so callers can compose
// them: most subcommands need both, and the second consumes the
// first.
//
// Module enumeration prefers `go.work` when present and falls back
// to a single root-only entry when no workspace exists. Discovery
// is consulted only when the user has not supplied an explicit
// module list via `.ergon.yaml` or a flag override.
package discover

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"go.thesmos.sh/ergon/internal/modules"
)

// Resolve bundles [Root] and [Modules]: discovers the repository
// root, then resolves the module set respecting override per the
// rules in [Modules]. Every multi-module ergon subcommand starts
// with a Resolve call so the discovery contract has exactly one
// implementation.
//
// Testdata handling tracks the source: when override is empty,
// `go.work` entries that contain a `testdata` segment are filtered
// out (the Go test toolchain reserves testdata for fixtures, never
// for release-eligible code). When override is non-empty, every
// entry is used verbatim — the user explicitly listed those
// directories, so testdata filtering is bypassed.
func Resolve(ctx context.Context, override []string) (string, []modules.Module, error) {
	root, err := Root(ctx)
	if err != nil {
		return "", nil, err
	}
	mods, err := Modules(root, override)
	if err != nil {
		return "", nil, err
	}
	return root, mods, nil
}

// ImportPath returns the module import path declared in
// <root>/go.mod — the value of the `module` directive. Used by gci
// to group imports of the project's own packages under the
// `prefix(<path>)` section.
//
// Errors wrap the underlying read failure when go.mod is missing
// or unreadable, and report a usage-style error when the file
// exists but contains no `module` directive (a malformed go.mod).
func ImportPath(root string) (string, error) {
	path := filepath.Join(root, "go.mod")
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	for line := range strings.Lines(string(body)) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") && !strings.HasPrefix(line, "module\t") {
			continue
		}
		mod := strings.TrimSpace(strings.TrimPrefix(line, "module"))
		mod = strings.Trim(mod, "\"")
		if mod != "" {
			return mod, nil
		}
	}
	return "", fmt.Errorf("no `module` directive in %s", path)
}

// Root returns the absolute path of the repository root by shelling
// out to `git rev-parse --show-toplevel`. Errors wrap the
// underlying command output so a non-git working directory produces
// a useful diagnostic.
func Root(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Modules returns the modules to operate on. When override is
// non-empty, every entry is wrapped in a [modules.Module] verbatim;
// the caller is responsible for the order. Otherwise Modules reads
// `<repoRoot>/go.work`, drops any `use` entry whose path contains
// a `testdata/` segment, and sorts the result root-first then
// lexicographically. When no workspace exists, returns a
// single-entry slice for the root module.
func Modules(repoRoot string, override []string) ([]modules.Module, error) {
	if len(override) > 0 {
		out := make([]modules.Module, 0, len(override))
		for _, dir := range override {
			out = append(out, modules.Module{Dir: normaliseUsePath(dir)})
		}
		return out, nil
	}
	return fromWorkspace(repoRoot)
}

// fromWorkspace reads `<repoRoot>/go.work` and returns the modules
// it declares. A missing workspace yields a single root entry; an
// empty workspace (no `use` entries that survive filtering)
// surfaces as an error so callers do not silently degrade.
func fromWorkspace(repoRoot string) ([]modules.Module, error) {
	workPath := filepath.Join(repoRoot, "go.work")
	body, err := os.ReadFile(workPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return []modules.Module{{Dir: "."}}, nil
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", workPath, err)
	}

	paths := parseGoWorkUses(string(body))
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s declares no `use` entries", workPath)
	}

	seen := map[string]struct{}{}
	out := make([]modules.Module, 0, len(paths))
	for _, raw := range paths {
		dir := normaliseUsePath(raw)
		if hasTestdataSegment(dir) {
			continue
		}
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, modules.Module{Dir: dir})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s declares only testdata entries", workPath)
	}
	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].Dir == "." && out[j].Dir != ".":
			return true
		case out[j].Dir == "." && out[i].Dir != ".":
			return false
		default:
			return out[i].Dir < out[j].Dir
		}
	})
	return out, nil
}

// parseGoWorkUses extracts the use-directive paths from a go.work
// file body. Handles both the block form
//
//	use (
//	    ./cli
//	    ./frontend/golang
//	)
//
// and the single-line form
//
//	use ./other
//
// Returns the raw path strings — normalisation to [modules.Module]
// dir happens in [normaliseUsePath].
//
// `//` line comments are stripped before parsing; quoted paths
// have their surrounding quotes removed.
func parseGoWorkUses(body string) []string {
	var out []string
	inBlock := false
	for line := range strings.SplitSeq(body, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !inBlock {
			switch {
			case line == "use (" || line == "use(":
				inBlock = true
			case strings.HasPrefix(line, "use "):
				path := strings.TrimSpace(strings.TrimPrefix(line, "use"))
				path = strings.Trim(path, "\"")
				if path != "" {
					out = append(out, path)
				}
			}
			continue
		}
		if line == ")" {
			inBlock = false
			continue
		}
		out = append(out, strings.Trim(line, "\""))
	}
	return out
}

// hasTestdataSegment reports whether dir contains a `testdata`
// segment at any depth. The Go test toolchain reserves `testdata/`
// for fixture trees, so any module whose path crosses one is a
// fixture (typically a multi-module test harness) and must not
// receive release tags.
func hasTestdataSegment(dir string) bool {
	return slices.Contains(strings.Split(dir, "/"), "testdata")
}

// normaliseUsePath converts a go.work use-directive path string
// (or a config-supplied path) into the form [modules.Module.Dir]
// expects: forward-slashes, no leading `./`, root represented as
// `.`.
func normaliseUsePath(raw string) string {
	p := filepath.ToSlash(strings.TrimSpace(raw))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "."
	}
	return p
}
