// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package branch

import (
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/checks/policy"
	"go.thesmos.sh/ergon/internal/style"
)

// TestPct pins the layer-stats percentage: the half-open
// interval [0, 100], with the zero-conditions sentinel mapped
// to 0 so the verdict renderer can detect the empty case.
func TestPct(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   layerStats
		want float64
	}{
		{layerStats{Total: 0, Covered: 0}, 0},
		{layerStats{Total: 10, Covered: 0}, 0},
		{layerStats{Total: 10, Covered: 5}, 50},
		{layerStats{Total: 10, Covered: 10}, 100},
	}
	for _, tc := range cases {
		if got := tc.in.Pct(); got != tc.want {
			t.Errorf("Pct(%+v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestRelativeFile pins the JSON Start → repo-relative path
// translation: file portion before the first colon, joined with
// the package's repo-relative directory; the root module's `.`
// prefix is elided.
func TestRelativeFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pkg, start, want string
	}{
		{"cli", "explain.go:4:8", "cli/explain.go"},
		{"backend/golang", "render.go:1:1", "backend/golang/render.go"},
		{".", "main.go:10:5", "main.go"},
		{"", "main.go:10:5", "main.go"},
		{"cli", "noColonHere", "cli/noColonHere"},
	}
	for _, tc := range cases {
		if got := relativeFile(tc.pkg, tc.start); got != tc.want {
			t.Errorf("relativeFile(%q, %q) = %q, want %q", tc.pkg, tc.start, got, tc.want)
		}
	}
}

// TestAggregateByLayer pins the per-layer accumulation rules:
//
//   - A condition is fully covered iff both TrueCount and
//     FalseCount are > 0 across every gobco run.
//   - Duplicate records (same RepoRel + Start) collapse to one
//     condition; counts sum across appearances.
//   - Excluded paths drop before counting.
//   - The longest-prefix declared layer claims each condition.
func TestAggregateByLayer(t *testing.T) {
	t.Parallel()

	packages := []coverage.Layer{
		{Path: "./...", Line: 0, Branch: 0},    // idx 0
		{Path: "cli/...", Line: 0, Branch: 80}, // idx 1
	}
	stats := []pkgStats{
		{
			Pkg: pkgInfo{ImportPath: "go.example.com/cli", Dir: "/r/cli", RepoRel: "cli"},
			Records: []condRecord{
				{Start: "a.go:1:1", TrueCount: 3, FalseCount: 2}, // covered
				{Start: "a.go:5:1", TrueCount: 1, FalseCount: 0}, // half
				{Start: "a.go:1:1", TrueCount: 1, FalseCount: 0}, // dup of #1; merged counts still cover
			},
		},
		{
			Pkg: pkgInfo{ImportPath: "go.example.com/root", Dir: "/r", RepoRel: "."},
			Records: []condRecord{
				{Start: "x.go:2:2", TrueCount: 2, FalseCount: 2}, // covered
			},
		},
	}

	out := aggregateByLayer(stats, packages, nil, nil, nil)
	// cli/... layer: 2 conditions (dup merged), 1 fully covered.
	if out[1].Total != 2 || out[1].Covered != 1 {
		t.Errorf("cli layer = %+v, want {Total:2, Covered:1}", out[1])
	}
	// ./... layer: 1 condition, 1 covered (the dot-prefix root).
	if out[0].Total != 1 || out[0].Covered != 1 {
		t.Errorf("./... layer = %+v, want {Total:1, Covered:1}", out[0])
	}
}

// TestAggregateByLayerExcludes pins the policy filter: a
// condition whose repo-relative file path matches an exclude is
// not counted under any layer.
func TestAggregateByLayerExcludes(t *testing.T) {
	t.Parallel()

	packages := []coverage.Layer{{Path: "cli/...", Line: 0, Branch: 80}}
	stats := []pkgStats{{
		Pkg: pkgInfo{ImportPath: "go.example.com/cli", Dir: "/r/cli", RepoRel: "cli"},
		Records: []condRecord{
			{Start: "a.go:1:1", TrueCount: 1, FalseCount: 1},
			{Start: "skip.go:2:2", TrueCount: 1, FalseCount: 1},
		},
	}}
	excludes := []policy.Exclude{{Path: "cli/skip.go", Reason: "test"}}

	out := aggregateByLayer(stats, packages, nil, excludes, nil)
	if out[0].Total != 1 || out[0].Covered != 1 {
		t.Errorf("layer = %+v, want {Total:1, Covered:1} (skip.go dropped)", out[0])
	}
}

// TestRenderTarget exercises the three verdict branches: pass,
// required fail, informational fail. The zero-conditions edge
// is also rendered as a dimmed dash, never as FAIL.
func TestRenderTarget(t *testing.T) {
	t.Parallel()

	t.Run("aggregate ≥ threshold renders PASS", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		layer := coverage.Layer{Path: "cli/...", Branch: 50, RequireBranch: true}
		failed := renderTarget(&buf, style.Style{}, layer, layerStats{Total: 10, Covered: 8})
		if failed {
			t.Fatalf("PASS path returned true: %q", buf.String())
		}
		if !strings.Contains(buf.String(), "PASS") {
			t.Fatalf("output missing PASS: %q", buf.String())
		}
	})

	t.Run("required + below threshold fails", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		layer := coverage.Layer{Path: "cli/...", Branch: 80, RequireBranch: true}
		failed := renderTarget(&buf, style.Style{}, layer, layerStats{Total: 10, Covered: 5})
		if !failed {
			t.Fatalf("required-fail returned false: %q", buf.String())
		}
		if !strings.Contains(buf.String(), "FAIL") {
			t.Fatalf("output missing FAIL: %q", buf.String())
		}
	})

	t.Run("informational + below threshold does not fail", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		layer := coverage.Layer{Path: "cli/...", Branch: 80, RequireBranch: false}
		failed := renderTarget(&buf, style.Style{}, layer, layerStats{Total: 10, Covered: 5})
		if failed {
			t.Fatalf("informational-fail returned true: %q", buf.String())
		}
		if !strings.Contains(buf.String(), "informational") {
			t.Fatalf("output missing informational note: %q", buf.String())
		}
	})

	t.Run("zero conditions in scope renders a dimmed dash", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		layer := coverage.Layer{Path: "cli/...", Branch: 80, RequireBranch: true}
		failed := renderTarget(&buf, style.Style{}, layer, layerStats{Total: 0, Covered: 0})
		if failed {
			t.Fatalf("empty-scope returned true: %q", buf.String())
		}
		if !strings.Contains(buf.String(), "no conditions in scope") {
			t.Fatalf("output missing empty notice: %q", buf.String())
		}
	})
}
