// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"slices"
	"strings"
	"testing"
)

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

// TestSelectTargets pins the layer-resolution rules used by the
// positional-argument filter: with no requested targets every
// declared layer survives; a request narrows to its longest-
// prefix layer; an unknown request drops silently.
func TestSelectTargets(t *testing.T) {
	t.Parallel()

	packages := []Layer{
		{Path: "internal/...", Line: 70},
		{Path: "internal/checks/...", Line: 80},
		{Path: "cmd/ergon/...", Line: 50},
	}

	t.Run("no request returns every declared layer", func(t *testing.T) {
		t.Parallel()
		got := selectTargets(packages, nil)
		if !slices.Equal(extractPaths(got), extractPaths(packages)) {
			t.Fatalf("got = %+v, want declared layers verbatim", got)
		}
	})

	t.Run("bare layer request: longest-prefix wins", func(t *testing.T) {
		t.Parallel()
		got := selectTargets(packages, []string{"internal/checks"})
		if len(got) != 1 {
			t.Fatalf("got = %+v, want exactly one layer", got)
		}
		if got[0].Path != "internal/checks/..." || got[0].Line != 80 {
			t.Errorf("got = %+v, want internal/checks/... at line 80", got[0])
		}
	})

	t.Run("subpath request narrows the layer's path", func(t *testing.T) {
		t.Parallel()
		got := selectTargets(packages, []string{"internal/checks/coverage"})
		if got[0].Path != "internal/checks/coverage/..." {
			t.Errorf("got Path = %q, want internal/checks/coverage/...", got[0].Path)
		}
		if got[0].Line != 80 {
			t.Errorf("got Line = %d, want 80 (inherited)", got[0].Line)
		}
	})
}

// TestSplitProfileHead pins the splitter the verbose uncovered-
// ranges path uses.
func TestSplitProfileHead(t *testing.T) {
	t.Parallel()

	path, span, ok := splitProfileHead("pkg/foo.go:12.5,18.13")
	if !ok || path != "pkg/foo.go" || span != "12.5,18.13" {
		t.Fatalf("split = (%q, %q, %v), want (pkg/foo.go, 12.5,18.13, true)", path, span, ok)
	}
}

// TestStripFileLocation pins the `go tool cover -func` path
// normaliser: the trailing `:<line>:` is removed.
func TestStripFileLocation(t *testing.T) {
	t.Parallel()

	if got := stripFileLocation("pkg/foo.go:12:"); got != "pkg/foo.go" {
		t.Errorf("stripFileLocation = %q, want pkg/foo.go", got)
	}
}

// extractPaths is a tiny helper that pulls the Path field from a
// layer slice for slice equality checks.
func extractPaths(ls []Layer) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Path
	}
	return out
}
