// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package mutation

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// writeModuleTree materialises a set of repo-relative paths under a
// fresh temp dir. A path ending in `/go.mod` becomes a module root;
// anything else is created as an empty file.
func writeModuleTree(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", p, err)
		}
		if err := os.WriteFile(full, []byte("module example.test\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return root
}

// TestNestedModules pins the boundary the mutation walk must stop at.
//
// gremlins is handed a path and walks the filesystem below it, with
// no notion of a module. On go.thesmos.sh/testkit that made the root
// layer mutate four workspace modules plus one tree absent from
// go.work: 4057 of 4544 mutants belonged elsewhere, and because
// their tests never ran, all of them counted "not covered" —
// 10.6% mutator coverage against 100% line coverage.
func TestNestedModules(t *testing.T) {
	t.Parallel()

	t.Run("finds every module rooted below the walk root", func(t *testing.T) {
		t.Parallel()
		root := writeModuleTree(t,
			"go.mod",
			"core/thing.go",
			"cmd/go.mod",
			"engine/go.mod",
			// Absent from any go.work, but still a module: this is
			// the tree that contributed 2494 mutants of pure noise.
			"gen/go.mod",
		)

		got, err := nestedModules(root, root)
		if err != nil {
			t.Fatalf("nestedModules: %v", err)
		}
		if want := []string{"cmd", "engine", "gen"}; !slices.Equal(got, want) {
			t.Errorf("nestedModules = %v, want %v", got, want)
		}
	})

	t.Run("the walk root's own go.mod is not a boundary", func(t *testing.T) {
		t.Parallel()
		root := writeModuleTree(t, "go.mod", "core/thing.go")
		got, err := nestedModules(root, root)
		if err != nil {
			t.Fatalf("nestedModules: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("nestedModules = %v, want none — excluding the module "+
				"being measured would mutate nothing at all", got)
		}
	})

	t.Run("a module inside a module is reported once", func(t *testing.T) {
		t.Parallel()
		root := writeModuleTree(t, "go.mod", "a/go.mod", "a/b/go.mod")
		got, err := nestedModules(root, root)
		if err != nil {
			t.Fatalf("nestedModules: %v", err)
		}
		// `a/b` is already covered by the `^a/` prefix; reporting it
		// too would double-count the same files in the disclosure.
		if want := []string{"a"}; !slices.Equal(got, want) {
			t.Errorf("nestedModules = %v, want %v", got, want)
		}
	})

	t.Run("directories Go ignores are not boundaries", func(t *testing.T) {
		t.Parallel()
		root := writeModuleTree(t,
			"go.mod",
			".cache/go.mod",
			"_scratch/go.mod",
			"vendor/example.com/dep/go.mod",
		)
		got, err := nestedModules(root, root)
		if err != nil {
			t.Fatalf("nestedModules: %v", err)
		}
		// A module under one of these is invisible to `go list ./...`,
		// so it cannot be a boundary the build respects.
		if len(got) != 0 {
			t.Errorf("nestedModules = %v, want none", got)
		}
	})

	t.Run("an unreadable directory fails the scan rather than the layer", func(t *testing.T) {
		t.Parallel()
		root := writeModuleTree(t, "go.mod", "core/thing.go")
		sealed := filepath.Join(root, "sealed")
		if err := os.Mkdir(sealed, 0o000); err != nil {
			t.Fatalf("mkdir sealed: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })

		// Surfaced rather than swallowed: a directory the scan cannot
		// read might hold a module, and silently treating it as empty
		// would mutate that module's files under a layer whose tests
		// never run — the exact miscount this scan exists to prevent,
		// reintroduced without a symptom.
		_, err := nestedModules(root, root)
		if err == nil {
			t.Skip("directory remained readable; nothing to assert on this platform")
		}
		if !strings.Contains(err.Error(), "nested modules") {
			t.Errorf("err = %v, want it to name the scan that failed", err)
		}
	})

	t.Run("paths are relative to the module root, not the walk root", func(t *testing.T) {
		t.Parallel()
		root := writeModuleTree(t, "go.mod", "core/sub/go.mod")

		// gremlins runs from the module root and matches its pattern
		// against paths relative to that, so a walk scoped to a
		// subtree must still anchor from the module root.
		got, err := nestedModules(root, filepath.Join(root, "core"))
		if err != nil {
			t.Fatalf("nestedModules: %v", err)
		}
		if want := []string{"core/sub"}; !slices.Equal(got, want) {
			t.Errorf("nestedModules = %v, want %v", got, want)
		}
	})
}

// TestWithNestedExclusions pins the regex handed to gremlins.
//
// Verified against gremlins directly before this was written: a
// single alternation behaves identically to repeated -E flags, and
// composes with a policy pattern — 482 runnable / 5 not covered
// either way, against 482 / 4062 unexcluded.
func TestWithNestedExclusions(t *testing.T) {
	t.Parallel()

	t.Run("no nested modules leaves the policy regex untouched", func(t *testing.T) {
		t.Parallel()
		if got := withNestedExclusions("(^|/)testdata/", nil); got != "(^|/)testdata/" {
			t.Errorf("withNestedExclusions = %q, want the base unchanged", got)
		}
	})

	t.Run("anchors each module at the start of the path", func(t *testing.T) {
		t.Parallel()
		got := withNestedExclusions("", []string{"cmd", "engine"})
		re, err := regexp.Compile(got)
		if err != nil {
			t.Fatalf("compile %q: %v", got, err)
		}
		for _, in := range []string{"cmd/main.go", "engine/run.go"} {
			if !re.MatchString(in) {
				t.Errorf("%q does not match %q, want it excluded", got, in)
			}
		}
		for _, in := range []string{"core/thing.go", "cmdline/x.go", "internal/cmd/x.go"} {
			if re.MatchString(in) {
				t.Errorf("%q matches %q, want it kept — only a path rooted "+
					"at the module is outside this layer", got, in)
			}
		}
	})

	t.Run("an alternating policy regex cannot bind across the join", func(t *testing.T) {
		t.Parallel()
		// Unparenthesised, `(^|/)testdata/|^gen/` reads as one four-way
		// alternation and the trailing branch of the policy pattern
		// would swallow the module anchor.
		got := withNestedExclusions("(^|/)testdata/|.*\\.gen\\.go", []string{"gen"})
		re, err := regexp.Compile(got)
		if err != nil {
			t.Fatalf("compile %q: %v", got, err)
		}
		for _, in := range []string{"a/testdata/x.go", "a/b.gen.go", "gen/x.go"} {
			if !re.MatchString(in) {
				t.Errorf("%q does not match %q, want every branch preserved", got, in)
			}
		}
		if re.MatchString("core/thing.go") {
			t.Errorf("%q matches core/thing.go, want it kept", got)
		}
	})
}
