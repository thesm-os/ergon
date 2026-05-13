// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"slices"
	"strings"
	"testing"
)

// TestGlobToRegex pins the glob-to-regex translation: `...` is the
// recursive wildcard, `*` is the single-segment wildcard, literal
// `.` characters are escaped.
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
// ones so [classify] finds the right threshold.
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

// TestClassify pins the contract of [classify]: passing functions
// increment Passing, below-threshold non-excluded functions land
// in Failures sorted by ascending coverage, excluded functions
// land in Excluded, unscoped functions land in Unscoped.
func TestClassify(t *testing.T) {
	t.Parallel()

	rows := []funcRow{
		{Path: "go.example.com/x/internal/checks/coverage/coverage.go", Func: "Run", Pct: 85.0},
		{Path: "go.example.com/x/internal/checks/coverage/coverage.go", Func: "weak", Pct: 40.0},
		{Path: "go.example.com/x/internal/version/version.go", Func: "Ignored", Pct: 0.0},
		{Path: "go.example.com/x/cmd/ergon/main.go", Func: "main", Pct: 55.0},
		{Path: "go.example.com/x/outside/scope.go", Func: "Stray", Pct: 0.0},
	}
	layers := compileLayers([]Layer{
		{Path: "internal/...", Line: 70},
		{Path: "internal/checks/...", Line: 80},
		{Path: "cmd/...", Line: 50},
	})
	excludes := compileExcludes([]Exclude{{Path: "internal/version/..."}})

	r := classify(rows, layers, excludes, "go.example.com/x/")
	if r.Passing != 2 {
		t.Errorf("Passing = %d, want 2 (Run + main)", r.Passing)
	}
	if r.Excluded != 1 {
		t.Errorf("Excluded = %d, want 1 (Ignored)", r.Excluded)
	}
	if r.Unscoped != 1 {
		t.Errorf("Unscoped = %d, want 1 (Stray)", r.Unscoped)
	}
	if len(r.Failures) != 1 || r.Failures[0].Func != "weak" {
		t.Fatalf("Failures = %+v, want [weak]", r.Failures)
	}
	if r.Failures[0].Layer != "internal/checks/..." {
		t.Errorf("Layer = %q, want internal/checks/... (longest prefix)", r.Failures[0].Layer)
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
