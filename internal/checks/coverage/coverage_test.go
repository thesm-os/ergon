// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"io"
	"os"
	"path/filepath"
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

// TestCollectUncoveredBlocks pins the parser the `uncovered`
// subcommand runs on the merged coverprofile: count=0 rows
// group by file, modulePrefix is stripped from each path, and
// blocks within a file sort by start line.
func TestCollectUncoveredBlocks(t *testing.T) {
	t.Parallel()

	merged := strings.Join([]string{
		"mode: atomic",
		"go.thesmos.sh/proj/pkg/a.go:10.1,15.2 3 0",
		"go.thesmos.sh/proj/pkg/a.go:20.1,22.5 1 5",
		"go.thesmos.sh/proj/pkg/a.go:5.1,9.2 2 0",
		"go.thesmos.sh/proj/pkg/b.go:1.1,4.2 4 0",
	}, "\n")
	got := collectUncoveredBlocks(merged, "go.thesmos.sh/proj/")
	if len(got) != 2 {
		t.Fatalf("files = %d, want 2", len(got))
	}
	a := got["pkg/a.go"]
	if len(a) != 2 {
		t.Fatalf("pkg/a.go blocks = %d, want 2 (covered row dropped)", len(a))
	}
	if a[0].StartLine != 5 || a[1].StartLine != 10 {
		t.Errorf("pkg/a.go order = %+v, want start lines 5,10", a)
	}
	b := got["pkg/b.go"]
	if len(b) != 1 || b[0].Stmts != 4 {
		t.Errorf("pkg/b.go = %+v, want one 4-stmt block", b)
	}
}

// TestUncovered exercises the top-level command path against a
// tempdir of synthetic profiles. Asserts the section header and
// per-file blocks render in the captured stdout.
func TestUncovered(t *testing.T) {
	t.Parallel()

	t.Run("empty coverage dir surfaces an error", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "missing")
		err := Uncovered(t.Context(), nil, io.Discard, io.Discard, "", dir, "")
		if err == nil {
			t.Fatal("Uncovered returned nil, want missing-profiles error")
		}
	})

	t.Run("merged profiles render grouped by file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		body := strings.Join([]string{
			"mode: atomic",
			"go.example.com/x/a.go:10.1,12.2 2 0",
			"go.example.com/x/b.go:1.1,3.2 1 5",
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, "root.out"), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		var stdout strings.Builder
		if err := Uncovered(t.Context(), nil, &stdout, io.Discard, "", dir, "go.example.com/x/"); err != nil {
			t.Fatalf("Uncovered err: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "a.go") {
			t.Fatalf("stdout missing a.go: %q", out)
		}
		if strings.Contains(out, "b.go") {
			t.Fatalf("stdout unexpectedly includes covered b.go: %q", out)
		}
		if !strings.Contains(out, "1 uncovered block") {
			t.Fatalf("stdout missing aggregate: %q", out)
		}
	})

	t.Run("fully covered profile renders the all-clear verdict", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		body := "mode: atomic\ngo.example.com/x/a.go:1.1,2.2 1 5\n"
		if err := os.WriteFile(filepath.Join(dir, "root.out"), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		var stdout strings.Builder
		if err := Uncovered(t.Context(), nil, &stdout, io.Discard, "", dir, "go.example.com/x/"); err != nil {
			t.Fatalf("Uncovered err: %v", err)
		}
		if !strings.Contains(stdout.String(), "no uncovered lines") {
			t.Fatalf("stdout missing all-clear: %q", stdout.String())
		}
	})
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
