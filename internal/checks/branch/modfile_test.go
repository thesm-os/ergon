// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package branch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"

	"go.thesmos.sh/ergon/internal/modules"
)

// The workspace fixture these tests resolve against — wsImports and
// wsOnDisk — lives in branch_test.go, which shares it.

// TestStageModfile covers the alternate go.mod that lets gobco's
// relocated copy resolve sibling workspace modules.
func TestStageModfile(t *testing.T) {
	t.Parallel()

	t.Run("no siblings needs no staging", func(t *testing.T) {
		t.Parallel()
		got, err := stageModfile(wsOnDisk(t), "alpha", nil, wsImports, t.TempDir())
		if err != nil {
			t.Fatalf("stageModfile: %v", err)
		}
		if got.Path != "" {
			t.Errorf("Path = %q, want empty so gobco runs without -modfile", got.Path)
		}
	})

	t.Run("writes an absolute replace and the require it needs", func(t *testing.T) {
		t.Parallel()
		root := wsOnDisk(t)
		tmp := t.TempDir()
		got, err := stageModfile(root, "beta",
			[]string{"example.test/ws/alpha"}, wsImports, tmp)
		if err != nil {
			t.Fatalf("stageModfile: %v", err)
		}
		if got.Path == "" {
			t.Fatal("Path is empty, want a staged modfile")
		}
		body, readErr := os.ReadFile(got.Path)
		if readErr != nil {
			t.Fatalf("read staged modfile: %v", readErr)
		}
		text := string(body)
		wantAbs := filepath.Join(root, "alpha")
		if !strings.Contains(text, "replace example.test/ws/alpha => "+wantAbs) {
			t.Errorf("staged modfile = %q, want an absolute replace to %q", text, wantAbs)
		}
		// A replace is inert without a require — the module has to be
		// in the build list for the replacement to apply at all.
		if !strings.Contains(text, "require example.test/ws/alpha") {
			t.Errorf("staged modfile = %q, want a require for the sibling", text)
		}
		// The real go.mod must be untouched.
		onDisk, _ := os.ReadFile(filepath.Join(root, "beta", "go.mod"))
		if strings.Contains(string(onDisk), "replace") {
			t.Errorf("real go.mod = %q, want it left alone", onDisk)
		}
	})

	t.Run("an existing require is preserved", func(t *testing.T) {
		t.Parallel()
		root := wsOnDisk(t)
		if err := os.WriteFile(filepath.Join(root, "beta", "go.mod"), []byte(
			"module example.test/ws/beta\n\ngo 1.26\n\n"+
				"require example.test/ws/alpha v1.4.0\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := stageModfile(root, "beta",
			[]string{"example.test/ws/alpha"}, wsImports, t.TempDir())
		if err != nil {
			t.Fatalf("stageModfile: %v", err)
		}
		body, _ := os.ReadFile(got.Path)
		if !strings.Contains(string(body), "v1.4.0") {
			t.Errorf("staged modfile = %q, want the declared version kept", body)
		}
	})

	t.Run("go.sum is copied alongside", func(t *testing.T) {
		t.Parallel()
		root := wsOnDisk(t)
		if err := os.WriteFile(filepath.Join(root, "beta", "go.sum"),
			[]byte("example.com/x v1.0.0 h1:abc=\n"), 0o600); err != nil {
			t.Fatalf("write go.sum: %v", err)
		}
		got, err := stageModfile(root, "beta",
			[]string{"example.test/ws/alpha"}, wsImports, t.TempDir())
		if err != nil {
			t.Fatalf("stageModfile: %v", err)
		}
		// Go derives the sum path from the modfile name, so external
		// deps stay verifiable on a cold module cache.
		sum := strings.TrimSuffix(got.Path, ".mod") + ".sum"
		if _, statErr := os.Stat(sum); statErr != nil {
			t.Errorf("no staged sum beside the modfile: %v", statErr)
		}
	})

	t.Run("a malformed go.mod surfaces", func(t *testing.T) {
		t.Parallel()
		root := wsOnDisk(t)
		if err := os.WriteFile(filepath.Join(root, "beta", "go.mod"),
			[]byte("this is not a go.mod {{{\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := stageModfile(root, "beta",
			[]string{"example.test/ws/alpha"}, wsImports, t.TempDir()); err == nil {
			t.Fatal("stageModfile returned nil, want the parse failure")
		}
	})
}

// TestInitialRequireVersion pins the synthesised require version. It
// is never resolved — a directory replacement supersedes it — but it
// has to agree with the path's major-version suffix or the go
// command rejects the file outright.
func TestInitialRequireVersion(t *testing.T) {
	t.Parallel()

	cases := []struct{ path, want string }{
		{"example.test/ws/alpha", "v0.0.0"},
		{"example.test/ws/alpha/v2", "v2.0.0"},
		{"example.test/ws/alpha/v17", "v17.0.0"},
		{"example.test/ws/alpha/v1", "v0.0.0"}, // v1 is not a suffix
		{"example.test/ws/alpha/v0", "v0.0.0"}, // nor is v0
		{"example.test/ws/verify", "v0.0.0"},   // not a version suffix
	}
	for _, tc := range cases {
		if got := initialRequireVersion(tc.path); got != tc.want {
			t.Errorf("initialRequireVersion(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestSiblingsFor pins the module-level union: one staged modfile has
// to satisfy every package in the module, so the sibling set is the
// union of each package's cross-module imports.
func TestSiblingsFor(t *testing.T) {
	t.Parallel()

	pkgs := []pkgInfo{
		{
			ImportPath: "example.test/ws/beta", RepoRel: "beta",
			Imports: []string{"example.test/ws/alpha"},
		},
		{
			ImportPath: "example.test/ws/beta/sub", RepoRel: "beta/sub",
			Imports: []string{"example.test/ws", "example.test/ws/alpha"},
		},
		// Belongs to alpha, so it must not contribute to beta's set.
		{
			ImportPath: "example.test/ws/alpha", RepoRel: "alpha",
			Imports: []string{"example.test/ws"},
		},
	}

	got := siblingsFor("beta", pkgs, wsImports)
	if len(got) != 2 {
		t.Fatalf("siblings = %v, want the deduplicated union of two", got)
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s] = true
	}
	if !seen["example.test/ws/alpha"] || !seen["example.test/ws"] {
		t.Errorf("siblings = %v, want both alpha and the root module", got)
	}

	if none := siblingsFor("alpha", []pkgInfo{pkgs[2]}, []modules.Import{
		{Dir: "alpha", ImportPath: "example.test/ws/alpha"},
	}); len(none) != 0 {
		t.Errorf("siblings = %v, want none when no other module is declared", none)
	}
}

// TestModuleSlug pins the staged-filename sanitiser. The slug is a
// security boundary as well as a naming scheme: it has to collapse a
// module directory to one path component so the staged file cannot
// escape the caller's temp directory.
func TestModuleSlug(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"alpha", "alpha"},
		{"engine/model", "engine_model"},
		{"Engine/Model", "Engine_Model"}, // uppercase preserved
		{"mod-v2/pkg9", "mod-v2_pkg9"},   // dashes and digits preserved
		{"a/../../etc", "a_______etc"},   // traversal flattened
		{".", "root"},                    // all-separator input
		{"", "root"},                     // empty input
		{"..", "root"},                   // nothing but dots
		{"a b", "a_b"},                   // whitespace replaced
	}
	for _, tc := range cases {
		got := moduleSlug(tc.in)
		if got != tc.want {
			t.Errorf("moduleSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Errorf("moduleSlug(%q) = %q, which is not a single safe component", tc.in, got)
		}
	}
}

// TestIsAllDigits pins the major-version suffix predicate.
func TestIsAllDigits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", true},
		{"17", true},
		{"2a", false},
		{"a2", false},
		{"1.0", false},
	} {
		if got := isAllDigits(tc.in); got != tc.want {
			t.Errorf("isAllDigits(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestRequires pins the require lookup that decides whether a
// synthesised requirement is needed.
func TestRequires(t *testing.T) {
	t.Parallel()

	f, err := modfile.Parse("go.mod", []byte(
		"module example.test/ws/beta\n\ngo 1.26\n\n"+
			"require example.test/other v1.2.3\n"), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !requires(f, "example.test/other") {
		t.Error("requires = false for a declared requirement, want true")
	}
	// The non-matching path exercises the loop's continue arm.
	if requires(f, "example.test/ws/alpha") {
		t.Error("requires = true for an undeclared requirement, want false")
	}

	empty, err := modfile.Parse("go.mod", []byte("module m\n\ngo 1.26\n"), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if requires(empty, "anything") {
		t.Error("requires = true on a modfile with no requires, want false")
	}
}

// TestSiblingsForDeduplicates covers the dedup arm: two packages in
// the same module importing the same sibling yield one entry, because
// one staged modfile serves the whole module.
func TestSiblingsForDeduplicates(t *testing.T) {
	t.Parallel()

	pkgs := []pkgInfo{
		{
			ImportPath: "example.test/ws/beta", RepoRel: "beta",
			Imports: []string{"example.test/ws/alpha"},
		},
		{
			ImportPath: "example.test/ws/beta/two", RepoRel: "beta/two",
			Imports: []string{"example.test/ws/alpha"},
		},
	}
	got := siblingsFor("beta", pkgs, wsImports)
	if len(got) != 1 || got[0] != "example.test/ws/alpha" {
		t.Errorf("siblings = %v, want the single deduplicated entry", got)
	}
}

// TestStageModfileUnknownSibling covers the guard for a sibling that
// is not a declared workspace module: there is no directory to point
// a replace at, so the entry is passed over rather than producing a
// bogus replace.
func TestStageModfileUnknownSibling(t *testing.T) {
	t.Parallel()

	root := wsOnDisk(t)
	got, err := stageModfile(root, "beta",
		[]string{"example.test/not-in-workspace"}, wsImports, t.TempDir())
	if err != nil {
		t.Fatalf("stageModfile: %v", err)
	}
	body, readErr := os.ReadFile(got.Path)
	if readErr != nil {
		t.Fatalf("read staged modfile: %v", readErr)
	}
	if strings.Contains(string(body), "not-in-workspace") {
		t.Errorf("staged modfile = %q, want the unknown sibling skipped", body)
	}
}
