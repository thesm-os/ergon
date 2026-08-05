// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package errorprefix

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/checks/policy"
)

// TestIsErrorsNew pins the syntactic detection: only the exact
// `errors.New` selector matches; aliases and unrelated selectors
// are rejected.
func TestIsErrorsNew(t *testing.T) {
	t.Parallel()

	cases := []struct {
		expr string
		want bool
	}{
		{"errors.New", true},
		{"fmt.Errorf", false},
		{"foo.New", false},
		{"errors.Is", false},
		{"errors", false},
		{"42", false},
	}
	for _, tc := range cases {
		expr, err := parser.ParseExpr(tc.expr + "()")
		if err != nil {
			t.Fatalf("ParseExpr(%q): %v", tc.expr, err)
		}
		call := expr.(*ast.CallExpr)
		if got := isErrorsNew(call.Fun); got != tc.want {
			t.Errorf("isErrorsNew(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// TestStringLiteral pins the string-literal extractor: only
// double-quoted string literals decode; integer literals, non-
// literals, and malformed escapes return ok=false.
func TestStringLiteral(t *testing.T) {
	t.Parallel()

	t.Run("quoted string decodes", func(t *testing.T) {
		t.Parallel()
		expr, _ := parser.ParseExpr(`"hello world"`)
		got, ok := stringLiteral(expr)
		if !ok || got != "hello world" {
			t.Fatalf("got %q ok=%v, want \"hello world\" ok=true", got, ok)
		}
	})

	t.Run("integer literal rejected", func(t *testing.T) {
		t.Parallel()
		expr, _ := parser.ParseExpr(`42`)
		_, ok := stringLiteral(expr)
		if ok {
			t.Fatal("ok=true, want false for non-string literal")
		}
	})

	t.Run("identifier rejected", func(t *testing.T) {
		t.Parallel()
		expr, _ := parser.ParseExpr(`foo`)
		_, ok := stringLiteral(expr)
		if ok {
			t.Fatal("ok=true, want false for identifier")
		}
	})

	t.Run("malformed quote rejected", func(t *testing.T) {
		t.Parallel()
		// Construct a BasicLit with an unparseable value directly —
		// parser would otherwise reject the input.
		bad := &ast.BasicLit{Kind: token.STRING, Value: `"\xZZ"`}
		_, ok := stringLiteral(bad)
		if ok {
			t.Fatal("ok=true, want false for malformed quoted literal")
		}
	})
}

// TestWithDefaultsExcludes pins that an empty Excludes list is a
// legitimate configuration rather than a missing one — the default
// is to exempt nothing.
func TestWithDefaultsExcludes(t *testing.T) {
	t.Parallel()
	if got := withDefaults(Config{}).Excludes; len(got) != 0 {
		t.Errorf("Excludes = %+v, want empty by default", got)
	}
}

// TestWithDefaults pins the merge: zero-value TargetDirs inherits
// the package defaults; a non-zero list stands.
func TestWithDefaults(t *testing.T) {
	t.Parallel()

	got := withDefaults(Config{})
	if len(got.TargetDirs) == 0 {
		t.Fatal("TargetDirs empty after withDefaults; want default list")
	}

	custom := Config{TargetDirs: []string{"foo"}}
	if got := withDefaults(custom); len(got.TargetDirs) != 1 || got.TargetDirs[0] != "foo" {
		t.Fatalf("TargetDirs = %+v, want [foo]", got.TargetDirs)
	}
}

// TestRun pins the contract of [Run]: errors.New literals must
// start with the file's package name (optionally `<pkg>.<sub>:`);
// test files are exempt; TargetDirs narrows the scan.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("matching prefix passes", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"clock: instant is zero\")\n",
		})

		var stdout bytes.Buffer
		if err := Run(&stdout, io.Discard, root, files, Defaults()); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "0 violations") {
			t.Fatalf("stdout = %q, want clean summary", stdout.String())
		}
	})

	t.Run("missing prefix is a violation", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"instant is zero\")\n",
		})

		var stderr bytes.Buffer
		err := Run(io.Discard, &stderr, root, files, Defaults())
		if err == nil {
			t.Fatal("Run returned nil, want error")
		}
		if !strings.Contains(stderr.String(), "clock: ") {
			t.Fatalf("stderr = %q, want it to name the expected prefix", stderr.String())
		}
	})

	t.Run("sub-package qualifier (<pkg>.<sub>:) is accepted", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/kernel.go": "package kernel\n\nimport \"errors\"\n\n" +
				"var ErrPatch = errors.New(\"kernel.patch: malformed\")\n",
		})

		if err := Run(io.Discard, io.Discard, root, files, Defaults()); err != nil {
			t.Fatalf("Run err: %v, want sub-package qualifier to pass", err)
		}
	})

	t.Run("test files are not scanned", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/clock_test.go": "package clock\n\nimport \"errors\"\n\n" +
				"var TestErr = errors.New(\"no prefix\")\n",
		})

		if err := Run(io.Discard, io.Discard, root, files, Defaults()); err != nil {
			t.Fatalf("Run err: %v, want test files to be exempt", err)
		}
	})

	t.Run("TargetDirs narrows the scan", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"foundation/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"clock: zero\")\n",
			"cmd/main.go": "package main\n\nimport \"errors\"\n\n" +
				"var ErrCmd = errors.New(\"anything goes here\")\n",
		})

		cfg := Config{TargetDirs: []string{"foundation"}}
		if err := Run(io.Discard, io.Discard, root, files, cfg); err != nil {
			t.Fatalf("Run err: %v, want cmd/ to be ignored", err)
		}
	})
}

// TestRunExcludes covers the exemption a fixture corpus needs.
//
// A generator's corpus deliberately holds the patterns the
// generator exists to reject — testkit's sentinel generator
// supports `prefix=off`, and proving that branch works needs a
// fixture whose sentinels genuinely lack the prefix. Without an
// exclusion the repository's own lint forces that fixture to be
// prefixed, at which point a generator ignoring `prefix=off`
// entirely would still produce a passing suite.
//
// TargetDirs cannot express it: a repo with root-level .go files
// can only reach them with `.`, which reaches the corpus too.
func TestRunExcludes(t *testing.T) {
	t.Parallel()

	corpus := map[string]string{
		"assert.go": "package testkit\n\nimport \"errors\"\n\n" +
			"var ErrFail = errors.New(\"testkit: assertion failed\")\n",
		"conformance/corpus/errors/noprefix/iface.go": "package noprefix\n\nimport \"errors\"\n\n" +
			"var ErrRetry = errors.New(\"retry the operation\")\n",
	}

	t.Run("without an exclude the corpus is a violation", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, corpus)
		if err := Run(io.Discard, io.Discard, root, files, Defaults()); err == nil {
			t.Fatal("Run returned nil; the fixture must violate, or the test proves nothing")
		}
	})

	t.Run("an excluded path is exempt and counted", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, corpus)
		cfg := Config{
			TargetDirs: []string{"."},
			Excludes: []policy.Exclude{{
				Path:   "conformance/corpus/...",
				Reason: "generator fixture corpus; violating patterns are the point",
			}},
		}

		var stdout bytes.Buffer
		if err := Run(&stdout, io.Discard, root, files, cfg); err != nil {
			t.Fatalf("Run err: %v, want the corpus exempt", err)
		}
		// The count is the whole difference between this and
		// checks.excludes, whose Reason surfaces nowhere: an
		// exemption nobody can see is how a lint quietly shrinks.
		if got := stdout.String(); !strings.Contains(got, "1 file(s) excluded") {
			t.Errorf("stdout = %q, want the exempted count reported", got)
		}
	})

	t.Run("the glob matches segments, not just a literal prefix", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, corpus)
		// A bare directory path is a literal under the schema's
		// glob syntax and must NOT sweep the tree beneath it — the
		// same rule checks.excludes follows.
		cfg := Config{Excludes: []policy.Exclude{{Path: "conformance/corpus"}}}
		if err := Run(io.Discard, io.Discard, root, files, cfg); err == nil {
			t.Fatal("Run returned nil; a bare directory must not match files beneath it")
		}
	})

	t.Run("a file outside TargetDirs is not counted as excluded", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, corpus)
		// Out of scope and exempt are different states. Counting
		// the former inflates the figure that exists to make
		// exemptions reviewable.
		cfg := Config{
			TargetDirs: []string{"conformance"},
			Excludes:   []policy.Exclude{{Path: "conformance/corpus/..."}},
		}
		var stdout bytes.Buffer
		if err := Run(&stdout, io.Discard, root, files, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if got := stdout.String(); !strings.Contains(got, "1 file(s) excluded") {
			t.Errorf("stdout = %q, want only the in-scope exemption counted", got)
		}
	})

	t.Run("no excludes reports no exclusion clause", func(t *testing.T) {
		t.Parallel()
		root, files := buildFiles(t, map[string]string{
			"pkg/clock.go": "package clock\n\nimport \"errors\"\n\n" +
				"var ErrZero = errors.New(\"clock: zero\")\n",
		})
		var stdout bytes.Buffer
		if err := Run(&stdout, io.Discard, root, files, Defaults()); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if strings.Contains(stdout.String(), "excluded") {
			t.Errorf("stdout = %q, want no exclusion clause when nothing was excluded", stdout.String())
		}
	})
}

// buildFiles writes files into a fresh tempdir and returns the
// root plus the list of repo-relative paths.
func buildFiles(t *testing.T, contents map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var paths []string
	for rel, body := range contents {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
		paths = append(paths, rel)
	}
	return root, paths
}
