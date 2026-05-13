// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"slices"
	"strings"
	"testing"
)

// TestGlobToRegex pins the glob-to-regex translation: `...` is
// the recursive wildcard, `*` is the single-segment wildcard,
// literal `.` characters are escaped.
func TestGlobToRegex(t *testing.T) {
	t.Parallel()

	cases := []struct {
		glob  string
		match []string
		miss  []string
	}{
		{
			glob:  "internal/checks/...",
			match: []string{"internal/checks/vuln", "internal/checks/coverage/coverage.go"},
			miss:  []string{"internal/lint", "cmd/ergon/main.go"},
		},
		{
			glob:  "**/*_test.go",
			match: []string{"pkg/foo_test.go", "deep/nested/bar_test.go"},
			miss:  []string{"pkg/foo.go"},
		},
		{
			glob:  "internal/version/...",
			match: []string{"internal/version/version.go"},
			miss:  []string{"internal/config/config.go"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.glob, func(t *testing.T) {
			t.Parallel()
			re := compileExcludes([]Exclude{{Path: tc.glob}})[0]
			for _, s := range tc.match {
				if !re.MatchString(s) {
					t.Errorf("%q did not match %q (regex %s)", tc.glob, s, re.String())
				}
			}
			for _, s := range tc.miss {
				if re.MatchString(s) {
					t.Errorf("%q unexpectedly matched %q (regex %s)", tc.glob, s, re.String())
				}
			}
		})
	}
}

// TestCompileLayers pins the longest-prefix-wins ordering of the
// compiled layers: more specific globs come before more general
// ones.
func TestCompileLayers(t *testing.T) {
	t.Parallel()

	layers := compileLayers([]Layer{
		{Path: "internal/...", Line: 70},
		{Path: "internal/checks/...", Line: 80},
		{Path: "cmd/...", Line: 50},
	})
	want := []string{"internal/checks/...", "internal/...", "cmd/..."}
	got := make([]string, len(layers))
	for i, l := range layers {
		got[i] = l.Path
	}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %+v, want %+v (longest path first)", got, want)
	}
}

// TestParseFuncLog pins the parser against representative
// `go tool cover -func` output.
func TestParseFuncLog(t *testing.T) {
	t.Parallel()

	out := strings.Join([]string{
		"go.example.com/x/pkg/foo.go:12:\tBar\t75.0%",
		"go.example.com/x/pkg/foo.go:34:\tBaz\t100.0%",
		"total:\t\t\t(statements)\t88.0%",
	}, "\n")
	rows := parseFuncLog(out)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (total: dropped)", len(rows))
	}
	if rows[0].Path != "go.example.com/x/pkg/foo.go" {
		t.Errorf("Path = %q, want go.example.com/x/pkg/foo.go", rows[0].Path)
	}
	if rows[0].Func != "Bar" || rows[0].Pct != 75.0 {
		t.Errorf("row[0] = %+v, want Bar 75.0", rows[0])
	}
}

// TestGlobMatch pins the shell-style glob the structural-skip
// matcher uses (`*` matches any run of characters, others
// literal).
func TestGlobMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pattern, input string
		want           bool
	}{
		{"*", "anything", true},
		{"Run*Suite", "RunBasicSuite", true},
		{"Run*Suite", "BasicSuite", false},
		{"*test/model.go*", "pkg/test/model.go", true},
		{"*test/model.go*", "pkg/test/other.go", false},
		{"_", "_", true},
		{"_", "named", false},
	}
	for _, tc := range cases {
		got := globMatch(tc.pattern, tc.input)
		if got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.input, got, tc.want)
		}
	}
}

// TestMatchesSkip pins the structural-skip rule: BOTH FuncGlob
// and FileGlob must match for a function to be skipped.
func TestMatchesSkip(t *testing.T) {
	t.Parallel()

	skips := []Skip{
		{Label: "suite", FuncGlob: "Run*Suite", FileGlob: "*test/*"},
		{Label: "model", FuncGlob: "*", FileGlob: "*test/model.go*"},
	}

	cases := []struct {
		fn, path string
		want     bool
	}{
		{"RunFooSuite", "pkg/test/suite.go", true}, // suite
		{"RunFooSuite", "pkg/main.go", false},      // file glob misses
		{"Validate", "pkg/test/model.go", true},    // model
		{"Validate", "pkg/main.go", false},         // neither
		{"BuildSuite", "pkg/test/internal/builder.go", false},
	}
	for _, tc := range cases {
		got := matchesSkip(tc.fn, tc.path, skips)
		if got != tc.want {
			t.Errorf("matchesSkip(%q, %q) = %v, want %v", tc.fn, tc.path, got, tc.want)
		}
	}
}
