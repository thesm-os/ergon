// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
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
// tempdir of synthetic profiles. Uses a fakeRunner that returns
// a canonical `go tool cover -func` output so the function-
// indexing path is hit without a real subprocess.
func TestUncovered(t *testing.T) {
	t.Parallel()

	t.Run("empty coverage dir surfaces an error", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "missing")
		err := Uncovered(t.Context(), &fakeCovRunner{}, io.Discard, io.Discard,
			"", dir, "", Config{}, nil, nil, UncoveredOptions{All: true})
		if err == nil {
			t.Fatal("Uncovered returned nil, want missing-profiles error")
		}
	})

	t.Run("--all renders every uncovered block grouped by function", func(t *testing.T) {
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
		runner := &fakeCovRunner{
			funcLog: "go.example.com/x/a.go:8:\tDoThing\t75.0%\n" +
				"go.example.com/x/b.go:1:\tCovered\t100.0%\n",
		}
		var stdout strings.Builder
		err := Uncovered(t.Context(), runner, &stdout, io.Discard,
			"", dir, "go.example.com/x/",
			Config{}, nil, nil, UncoveredOptions{All: true})
		if err != nil {
			t.Fatalf("Uncovered err: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "a.go") {
			t.Fatalf("stdout missing a.go: %q", out)
		}
		if !strings.Contains(out, "DoThing") {
			t.Fatalf("stdout missing containing function name: %q", out)
		}
		if strings.Contains(out, "b.go") {
			t.Fatalf("stdout unexpectedly includes covered b.go: %q", out)
		}
		if !strings.Contains(out, "1 uncovered block") {
			t.Fatalf("stdout missing aggregate: %q", out)
		}
	})

	t.Run("default mode drops files outside the configured layers", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		body := strings.Join([]string{
			"mode: atomic",
			"go.example.com/x/internal/a.go:10.1,12.2 2 0",
			"go.example.com/x/cmd/b.go:5.1,8.2 1 0",
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, "root.out"), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		runner := &fakeCovRunner{
			funcLog: "go.example.com/x/internal/a.go:8:\tInternalFn\t75.0%\n" +
				"go.example.com/x/cmd/b.go:5:\tCmdFn\t0.0%\n",
		}
		cfg := Config{Packages: []Layer{{Path: "internal/...", Line: 70}}}
		var stdout strings.Builder
		err := Uncovered(t.Context(), runner, &stdout, io.Discard,
			"", dir, "go.example.com/x/",
			cfg, nil, nil, UncoveredOptions{})
		if err != nil {
			t.Fatalf("Uncovered err: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "internal/a.go") {
			t.Fatalf("internal/a.go missing under default filter: %q", out)
		}
		if strings.Contains(out, "cmd/b.go") {
			t.Fatalf("cmd/b.go leaked past layer filter: %q", out)
		}
	})

	t.Run("fully covered profile renders the all-clear verdict", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		body := "mode: atomic\ngo.example.com/x/a.go:1.1,2.2 1 5\n"
		if err := os.WriteFile(filepath.Join(dir, "root.out"), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		runner := &fakeCovRunner{funcLog: "go.example.com/x/a.go:1:\tA\t100.0%\n"}
		var stdout strings.Builder
		err := Uncovered(t.Context(), runner, &stdout, io.Discard,
			"", dir, "go.example.com/x/",
			Config{}, nil, nil, UncoveredOptions{All: true})
		if err != nil {
			t.Fatalf("Uncovered err: %v", err)
		}
		if !strings.Contains(stdout.String(), "no uncovered lines") {
			t.Fatalf("stdout missing all-clear: %q", stdout.String())
		}
	})
}

// TestIndexFunctionsByFile pins the parser for
// `go tool cover -func` output: per-file function lists are
// sorted by start line, the `total:` row is dropped, and the
// path is module-prefix-stripped.
func TestIndexFunctionsByFile(t *testing.T) {
	t.Parallel()

	funcLog := strings.Join([]string{
		"go.example.com/x/a.go:42:\tLater\t75.0%",
		"go.example.com/x/a.go:10:\tEarlier\t50.0%",
		"go.example.com/x/b.go:1:\tBeenAround\t100.0%",
		"total:\t\t\t88.0%",
	}, "\n")
	got := indexFunctionsByFile(funcLog, "go.example.com/x/")
	if len(got) != 2 {
		t.Fatalf("files = %d, want 2", len(got))
	}
	a := got["a.go"]
	want := []funcSpan{{StartLine: 10, Func: "Earlier"}, {StartLine: 42, Func: "Later"}}
	if !slices.Equal(a, want) {
		t.Errorf("a.go spans = %+v, want %+v", a, want)
	}
}

// TestFunctionAt pins the lookup that maps a line to its
// containing function: the latest span with StartLine ≤ line.
func TestFunctionAt(t *testing.T) {
	t.Parallel()

	spans := []funcSpan{
		{StartLine: 10, Func: "First"},
		{StartLine: 30, Func: "Second"},
		{StartLine: 60, Func: "Third"},
	}
	cases := []struct {
		line int
		want string
	}{
		{15, "First"},
		{30, "Second"},
		{55, "Second"},
		{60, "Third"},
		{1000, "Third"},
		{5, ""},
	}
	for _, tc := range cases {
		if got := functionAt(spans, tc.line); got != tc.want {
			t.Errorf("functionAt(%d) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// fakeCovRunner satisfies [xexec.Runner] for the coverage tests
// that exercise the `go tool cover -func` shell-out. funcLog is
// returned verbatim via opts.Stdout; LookPath is unused.
type fakeCovRunner struct {
	funcLog string
}

func (f *fakeCovRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	if opts.Stdout != nil && f.funcLog != "" {
		_, _ = opts.Stdout.Write([]byte(f.funcLog))
	}
	return nil
}

func (*fakeCovRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
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
