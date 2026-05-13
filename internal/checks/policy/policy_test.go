// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package policy

import (
	"regexp"
	"strings"
	"testing"
)

// TestExclude is a placeholder hook; [Exclude] carries no methods
// today, but pinning the field shape here keeps the schema honest
// against accidental mapstructure-tag drift.
func TestExclude(t *testing.T) {
	t.Parallel()

	e := Exclude{Path: "internal/version/...", Reason: "ldflags-injected"}
	if e.Path == "" || e.Reason == "" {
		t.Fatalf("Exclude zero-value or missing fields: %+v", e)
	}
}

// TestSkip mirrors [TestExclude] for [Skip].
func TestSkip(t *testing.T) {
	t.Parallel()

	s := Skip{Label: "suite", FuncGlob: "Run*Suite", FileGlob: "*test/*"}
	if s.Label == "" || s.FuncGlob == "" || s.FileGlob == "" {
		t.Fatalf("Skip zero-value or missing fields: %+v", s)
	}
}

// TestMatchesExclude pins the path-glob exclusion semantics: `...`
// matches any sequence of segments, `*` matches a single segment,
// no globs match nothing.
func TestMatchesExclude(t *testing.T) {
	t.Parallel()

	excludes := []Exclude{
		{Path: "internal/version/...", Reason: "ldflags"},
		{Path: "**/*_test.go", Reason: "tests"},
	}

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"recursive subdir matches", "internal/version/build.go", true},
		{"deeper recursive match", "internal/version/sub/pkg/x.go", true},
		{"test file matches via star", "internal/checks/coverage/coverage_test.go", true},
		{"unrelated path misses", "internal/checks/coverage/coverage.go", false},
		{"empty excludes never match", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MatchesExclude(tc.path, excludes)
			if got != tc.want {
				t.Errorf("MatchesExclude(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestMatchesSkip pins the AND-semantic of [Skip]: a hit requires
// both the function name AND the file path to match.
func TestMatchesSkip(t *testing.T) {
	t.Parallel()

	skips := []Skip{
		{Label: "suite", FuncGlob: "Run*Suite", FileGlob: "*test/*"},
		{Label: "model", FuncGlob: "*", FileGlob: "*test/model.go*"},
	}

	t.Run("both globs match: skip", func(t *testing.T) {
		t.Parallel()
		if !MatchesSkip("RunFooSuite", "pkg/test/suite.go", skips) {
			t.Fatal("expected RunFooSuite in pkg/test/ to be skipped")
		}
	})

	t.Run("file glob alone is not enough", func(t *testing.T) {
		t.Parallel()
		if MatchesSkip("ValidateFoo", "pkg/test/suite.go", skips[:1]) {
			t.Fatal("expected non-Run*Suite function in test dir NOT to be skipped")
		}
	})

	t.Run("func glob alone is not enough", func(t *testing.T) {
		t.Parallel()
		if MatchesSkip("RunFooSuite", "pkg/main.go", skips[:1]) {
			t.Fatal("expected Run*Suite outside test dir NOT to be skipped")
		}
	})

	t.Run("star func glob matches every function", func(t *testing.T) {
		t.Parallel()
		if !MatchesSkip("Anything", "pkg/test/model.go", skips) {
			t.Fatal("expected any function in *test/model.go to match the model skip")
		}
	})
}

// TestGremlinsExcludeRegex pins the gremlins-flag composition: the
// joined regex matches any file the policy excludes (paths) or
// skips (file globs only), and stays empty when the policy is.
func TestGremlinsExcludeRegex(t *testing.T) {
	t.Parallel()

	t.Run("empty policy returns empty regex", func(t *testing.T) {
		t.Parallel()
		if got := GremlinsExcludeRegex(nil, nil); got != "" {
			t.Fatalf("regex = %q, want empty", got)
		}
	})

	t.Run("excludes and skips both contribute file patterns", func(t *testing.T) {
		t.Parallel()
		excludes := []Exclude{{Path: "*_string.go"}}
		skips := []Skip{{Label: "model", FuncGlob: "*", FileGlob: "*test/model.go"}}
		got := GremlinsExcludeRegex(excludes, skips)
		re, err := regexp.Compile(got)
		if err != nil {
			t.Fatalf("compile %q: %v", got, err)
		}
		// Exclude side: the stringer-generated file is filtered.
		if !re.MatchString("/abs/path/foo_string.go") {
			t.Errorf("stringer file did not match %q", got)
		}
		// Skip side: the model file is filtered.
		if !re.MatchString("/abs/path/pkg/test/model.go") {
			t.Errorf("model file did not match %q", got)
		}
		// Unrelated file is NOT filtered.
		if re.MatchString("/abs/path/pkg/coverage.go") {
			t.Errorf("unrelated file unexpectedly matched %q", got)
		}
	})

	t.Run("empty Path / FileGlob entries are dropped", func(t *testing.T) {
		t.Parallel()
		got := GremlinsExcludeRegex(
			[]Exclude{{Path: ""}, {Path: "*.gen.go"}},
			[]Skip{{Label: "x", FuncGlob: "*", FileGlob: ""}},
		)
		if !strings.Contains(got, `\.gen\.go`) {
			t.Errorf("regex = %q, want \\.gen\\.go fragment", got)
		}
	})
}

// TestGlobRegex pins the path-glob → regex translation used by
// coverage's per-function classifier.
func TestGlobRegex(t *testing.T) {
	t.Parallel()

	cases := []struct {
		glob, want string
	}{
		{"internal/...", `^internal/.*$`},
		{"*.gen.go", `^.*\.gen\.go$`},
		{"cmd/ergon/...", `^cmd/ergon/.*$`},
	}
	for _, tc := range cases {
		if got := GlobRegex(tc.glob); got != tc.want {
			t.Errorf("GlobRegex(%q) = %q, want %q", tc.glob, got, tc.want)
		}
	}
}
