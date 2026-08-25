// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/checks/policy"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/style"
)

// testImports is the canonical single-module fixture
// (`go.example.com/x` mapped to the repo root) used across the
// per-call tests. Mirrors the shape `discover.ModuleImports`
// returns for a single-module repo.
var testImports = []modules.Import{{Dir: ".", ImportPath: "go.example.com/x"}}

// testProjImports mirrors testImports for the `go.thesmos.sh/proj`
// fixture used by [TestCollectUncoveredBlocks].
var testProjImports = []modules.Import{{Dir: ".", ImportPath: "go.thesmos.sh/proj"}}

// TestToRepoRelative pins the multi-module translation: each
// import is matched longest-prefix-first, the root module strips
// to a leading-slash-free path, and a token with no matching
// module surfaces verbatim so misconfiguration is visible.
func TestToRepoRelative(t *testing.T) {
	t.Parallel()

	imports := sortedPrefixes([]modules.Import{
		{Dir: ".", ImportPath: "go.example.com/proj"},
		{Dir: "cli", ImportPath: "go.example.com/proj/cli"},
		{Dir: "backend/golang", ImportPath: "go.example.com/backend"},
	})

	cases := []struct {
		in   string
		want string
	}{
		// Root module — strips, leading slash gone.
		{"go.example.com/proj/internal/foo.go", "internal/foo.go"},
		// Nested module wins over its parent (longest-prefix-first).
		{"go.example.com/proj/cli/internal/bar.go", "cli/internal/bar.go"},
		// Diverging submodule (different import-path tree) maps to
		// the workspace-relative path it actually lives at.
		{"go.example.com/backend/pkg/baz.go", "backend/golang/pkg/baz.go"},
		// Exact-import-path with no path tail.
		{"go.example.com/proj", ""},
		{"go.example.com/backend", "backend/golang"},
		// Unknown module surfaces verbatim — misconfiguration
		// stays visible instead of silently mis-classifying.
		{"go.somewhere/else/qux.go", "go.somewhere/else/qux.go"},
	}
	for _, tc := range cases {
		if got := toRepoRelative(imports, tc.in); got != tc.want {
			t.Errorf("toRepoRelative(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestClaimRows pins the specificity-wins rule: each row claims
// the longest-prefix declared layer, so nested layers override
// their parents instead of stacking.
func TestClaimRows(t *testing.T) {
	t.Parallel()

	packages := []Layer{
		{Path: "./...", Line: 50},
		{Path: "backend/...", Line: 90},
		{Path: "backend/golang/...", Line: 80},
		{Path: "cli/...", Line: 60},
	}
	imports := []modules.Import{
		{Dir: ".", ImportPath: "go.example.com/proj"},
		{Dir: "backend/golang", ImportPath: "go.example.com/proj/backend/golang"},
	}
	rows := []funcRow{
		{Path: "go.example.com/proj/eidostest/foo.go", Func: "A"},
		{Path: "go.example.com/proj/backend/protobuf/foo.go", Func: "B"},
		{Path: "go.example.com/proj/backend/golang/foo.go", Func: "C"},
		{Path: "go.example.com/proj/cli/foo.go", Func: "D"},
	}
	got := claimRows(packages, sortedPrefixes(imports), rows)
	want := []int{0, 1, 2, 3} // ./..., backend/..., backend/golang/..., cli/...
	if !slices.Equal(got, want) {
		t.Fatalf("claims = %+v, want %+v", got, want)
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
		got, _ := SelectTargets(packages, nil)
		if !slices.Equal(extractPaths(got), extractPaths(packages)) {
			t.Fatalf("got = %+v, want declared layers verbatim", got)
		}
	})

	t.Run("bare layer request: longest-prefix wins", func(t *testing.T) {
		t.Parallel()
		got, _ := SelectTargets(packages, []string{"internal/checks"})
		if len(got) != 1 {
			t.Fatalf("got = %+v, want exactly one layer", got)
		}
		if got[0].Path != "internal/checks/..." || got[0].Line != 80 {
			t.Errorf("got = %+v, want internal/checks/... at line 80", got[0])
		}
	})

	t.Run("subpath request narrows the layer's path", func(t *testing.T) {
		t.Parallel()
		got, _ := SelectTargets(packages, []string{"internal/checks/coverage"})
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

	if _, _, ok := splitProfileHead("no-colon-here"); ok {
		t.Fatal("ok=true for colon-less input, want false")
	}
	if _, _, ok := splitProfileHead(":leading"); ok {
		t.Fatal("ok=true for leading colon, want false")
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
	got := collectUncoveredBlocks(merged, testProjImports)
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
			"", dir, nil, Config{}, nil, nil, UncoveredOptions{All: true})
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
			"", dir, testImports,
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
			"", dir, testImports,
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
			"", dir, testImports,
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
	got := indexFunctionsByFile(funcLog, testImports)
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

// TestDefaults pins the [Config] defaults: empty Packages so the
// check short-circuits with a notice until thresholds are
// declared, and TopN=10 for the failing-function list.
func TestDefaults(t *testing.T) {
	t.Parallel()

	got := Defaults()
	if len(got.Packages) != 0 {
		t.Fatalf("Defaults().Packages = %+v, want empty", got.Packages)
	}
	if got.TopN != 10 {
		t.Fatalf("Defaults().TopN = %d, want 10", got.TopN)
	}
}

// TestWithDefaults pins the per-call merge: a Config with zero
// TopN inherits [Defaults]'s TopN; a non-zero TopN is preserved.
func TestWithDefaults(t *testing.T) {
	t.Parallel()

	if got := withDefaults(Config{}).TopN; got != 10 {
		t.Fatalf("zero TopN merged = %d, want 10", got)
	}
	if got := withDefaults(Config{TopN: 5}).TopN; got != 5 {
		t.Fatalf("non-zero TopN merged = %d, want 5", got)
	}
}

// TestUniqueFiles pins the path-dedup helper used by the verbose
// uncovered-ranges block: first-occurrence order is preserved
// and duplicate paths are dropped.
func TestUniqueFiles(t *testing.T) {
	t.Parallel()

	in := []Failure{
		{Path: "a.go"}, {Path: "b.go"}, {Path: "a.go"}, {Path: "c.go"},
	}
	got := uniqueFiles(in)
	want := []string{"a.go", "b.go", "c.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("uniqueFiles = %+v, want %+v", got, want)
	}
}

// TestWriteUncoveredRanges pins the verbose-mode renderer: for
// every failing-file's `count=0` block in the merged profile,
// one indented `file:start-end (stmts)` line surfaces.
func TestWriteUncoveredRanges(t *testing.T) {
	t.Parallel()

	merged := strings.Join([]string{
		"mode: atomic",
		"go.example.com/x/a.go:5.1,9.2 2 0",
		"go.example.com/x/b.go:1.1,3.2 1 0",
	}, "\n")
	failures := []Failure{{Path: "a.go"}}
	var buf strings.Builder
	writeUncoveredRanges(&buf, style.Style{}, failures, testImports, merged)
	out := buf.String()
	if !strings.Contains(out, "Uncovered ranges") {
		t.Fatalf("output missing header: %q", out)
	}
	if !strings.Contains(out, "a.go:5-9") {
		t.Fatalf("output missing a.go range: %q", out)
	}
	if strings.Contains(out, "b.go") {
		t.Fatalf("b.go leaked into the failing-file-only block: %q", out)
	}
}

// TestRenderTarget exercises the per-target classifier: a row
// matching the layer prefix below the threshold surfaces as a
// failure; an excluded row counts under "excluded"; a skipped
// row counts under "skipped"; a passing row counts toward
// "passing". Returns true when at least one failure surfaces.
func TestRenderTarget(t *testing.T) {
	t.Parallel()

	packages := []Layer{{Path: "internal/...", Line: 80}}
	layer := packages[0]
	rows := []funcRow{
		{Path: "go.example.com/x/internal/a.go", Func: "Pass", Pct: 100},
		{Path: "go.example.com/x/internal/b.go", Func: "Fail", Pct: 50},
		{Path: "go.example.com/x/cmd/c.go", Func: "Outside", Pct: 0},
	}
	claims := claimRows(packages, testImports, rows)
	// Aggregate below the threshold so the layer fails the new
	// aggregate-based verdict.
	agg := layerStats{TotalStmts: 100, CoveredStmts: 50}
	var buf strings.Builder
	failed := renderTarget(&buf, style.Style{}, layer, 0, rows, claims, agg,
		nil, nil, testImports, 10, false, "")
	if !failed {
		t.Fatal("renderTarget returned false, want true (aggregate below threshold)")
	}
	out := buf.String()
	for _, want := range []string{"internal", "Fail", "FAIL", "50.0%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "Outside") {
		t.Fatalf("function outside the layer prefix leaked into output: %q", out)
	}
}

// TestRenderTargetSpecificityWins pins the claim-based row
// filter: a row claimed by a nested declared layer is excluded
// from its parent's report even when both share the prefix.
func TestRenderTargetSpecificityWins(t *testing.T) {
	t.Parallel()

	packages := []Layer{
		{Path: "internal/...", Line: 80},        // idx 0 — the parent
		{Path: "internal/checks/...", Line: 90}, // idx 1 — the nested
	}
	rows := []funcRow{
		{Path: "go.example.com/x/internal/foo.go", Func: "Parent", Pct: 100},
		{Path: "go.example.com/x/internal/checks/bar.go", Func: "Nested", Pct: 100},
	}
	claims := claimRows(packages, testImports, rows)

	var buf strings.Builder
	renderTarget(&buf, style.Style{}, packages[0], 0, rows, claims,
		layerStats{TotalStmts: 10, CoveredStmts: 10},
		nil, nil, testImports, 10, false, "")
	out := buf.String()
	// Only the Parent row is claimed by the parent layer: 1 row
	// survives, all at 100% so it counts as passing.
	if !strings.Contains(out, "1 ≥ threshold") {
		t.Fatalf("parent layer should claim one passing row: %q", out)
	}
	if strings.Contains(out, "Nested") {
		t.Fatalf("nested-claimed row leaked into parent's report: %q", out)
	}
}

// TestRenderTargetPolicyAndPrefix pins the three remaining row-
// dispositions: a path-prefix mismatch is dropped (narrowed
// target case), an exclude moves the row to the "excluded"
// counter, and a skip moves it to "skipped". All three keep the
// row out of the failures list.
func TestRenderTargetPolicyAndPrefix(t *testing.T) {
	t.Parallel()

	packages := []Layer{{Path: "internal/...", Line: 80}}
	// Narrow the target to a sub-prefix the way positional-arg
	// resolution does: layer.Path = `internal/foo/...` derived
	// from the declared `internal/...`.
	narrowed := Layer{Path: "internal/foo/...", Line: 80}
	rows := []funcRow{
		{Path: "go.example.com/x/internal/foo/a.go", Func: "Belongs", Pct: 50},
		{Path: "go.example.com/x/internal/bar/b.go", Func: "WrongPrefix", Pct: 50},
		{Path: "go.example.com/x/internal/foo/c.go", Func: "Excluded", Pct: 50},
		{Path: "go.example.com/x/internal/foo/d.go", Func: "Skipped", Pct: 50},
	}
	claims := claimRows(packages, testImports, rows)
	excludes := []policy.Exclude{{Path: "internal/foo/c.go", Reason: "test"}}
	skips := []policy.Skip{{Label: "marker", FuncGlob: "Skipped", FileGlob: "internal/foo/d.go"}}

	var buf strings.Builder
	// Aggregate set to fail so the layer reports below + excluded
	// + skipped counters; the WrongPrefix row drops before any
	// counter increments.
	renderTarget(&buf, style.Style{}, narrowed, 0, rows, claims,
		layerStats{TotalStmts: 10, CoveredStmts: 5},
		excludes, skips, testImports, 10, false, "")
	out := buf.String()
	if !strings.Contains(out, "1 ≥ threshold") {
		// Belongs is below the layer's 80% line, so it counts as
		// `below`, not `≥ threshold`. We assert on the other
		// counters; this stays here as a smoke check.
		t.Logf("note: 0 passing rows expected at this threshold; got %q", out)
	}
	if !strings.Contains(out, "1 below") {
		t.Fatalf("want 1 below (Belongs at 50%%): %q", out)
	}
	if !strings.Contains(out, "1 excluded") {
		t.Fatalf("want 1 excluded: %q", out)
	}
	if !strings.Contains(out, "1 skipped") {
		t.Fatalf("want 1 skipped: %q", out)
	}
	if strings.Contains(out, "WrongPrefix") {
		t.Fatalf("out-of-prefix row leaked: %q", out)
	}
}

// TestRun exercises the top-level orchestrator path: given a
// synthetic coverprofile and a fake runner that returns a
// matching funcLog, the function emits per-target sections and
// returns nil when every gated function passes.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("passing run returns nil and renders the final verdict", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		profile := "mode: atomic\ngo.example.com/x/internal/a.go:1.1,2.2 1 5\n"
		if err := os.WriteFile(filepath.Join(dir, "root.out"), []byte(profile), 0o600); err != nil {
			t.Fatalf("write profile: %v", err)
		}
		runner := &fakeCovRunner{
			funcLog: "go.example.com/x/internal/a.go:1:\tA\t100.0%\n",
		}
		cfg := Config{Packages: []Layer{{Path: "internal/...", Line: 70}}}
		var stdout strings.Builder
		err := Run(t.Context(), runner, &stdout, io.Discard,
			"", dir, testImports, cfg, nil, nil, RunOptions{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "every gated function") {
			t.Fatalf("output missing pass verdict: %q", stdout.String())
		}
	})

	t.Run("function below threshold surfaces as a failure", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		profile := "mode: atomic\ngo.example.com/x/internal/a.go:5.1,9.2 2 0\n"
		if err := os.WriteFile(filepath.Join(dir, "root.out"), []byte(profile), 0o600); err != nil {
			t.Fatalf("write profile: %v", err)
		}
		runner := &fakeCovRunner{
			funcLog: "go.example.com/x/internal/a.go:1:\tA\t0.0%\n",
		}
		cfg := Config{Packages: []Layer{{Path: "internal/...", Line: 70}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard,
			"", dir, testImports, cfg, nil, nil, RunOptions{})
		if err == nil {
			t.Fatal("Run returned nil, want failure")
		}
	})

	t.Run("empty packages short-circuits with a skip notice", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "root.out"), []byte("mode: atomic\n"), 0o600); err != nil {
			t.Fatalf("write profile: %v", err)
		}
		runner := &fakeCovRunner{}
		var stdout strings.Builder
		err := Run(t.Context(), runner, &stdout, io.Discard,
			"", dir, nil, Config{}, nil, nil, RunOptions{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "skipped") {
			t.Fatalf("stdout missing skip notice: %q", stdout.String())
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
	if got := stripFileLocation("no-colon"); got != "no-colon" {
		t.Errorf("stripFileLocation no-colon = %q, want no-colon (input returned)", got)
	}
	if got := stripFileLocation("foo:bar"); got != "foo" {
		t.Errorf("stripFileLocation foo:bar = %q, want foo", got)
	}
}

// TestStripPathToLayerPrefix pins the directory-prefix extractor
// used to compare a cover-output file path against the configured
// layer paths.
func TestStripPathToLayerPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"internal/foo/bar.go", "internal/foo"},
		{"foo.go", "foo.go"},
		{"a/b/c/d.go", "a/b/c"},
	}
	for _, tc := range cases {
		if got := stripPathToLayerPrefix(tc.in); got != tc.want {
			t.Errorf("stripPathToLayerPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSplitFuncCoverHead pins the `<path>:<line>:` token parser
// `parseFuncLog` uses: succeeds on canonical tokens, returns
// ok=false on inputs without a parseable line number.
func TestSplitFuncCoverHead(t *testing.T) {
	t.Parallel()

	t.Run("canonical token parses", func(t *testing.T) {
		t.Parallel()
		path, line, ok := splitFuncCoverHead("pkg/foo.go:12:")
		if !ok || path != "pkg/foo.go" || line != 12 {
			t.Fatalf("split = (%q, %d, %v), want (pkg/foo.go, 12, true)", path, line, ok)
		}
	})

	t.Run("token without line number rejected", func(t *testing.T) {
		t.Parallel()
		if _, _, ok := splitFuncCoverHead("no-colon-here"); ok {
			t.Fatal("ok=true, want false")
		}
	})

	t.Run("non-integer line number rejected", func(t *testing.T) {
		t.Parallel()
		if _, _, ok := splitFuncCoverHead("pkg/foo.go:abc:"); ok {
			t.Fatal("ok=true, want false")
		}
	})
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

// errCovRunner is a [xexec.Runner] that always fails, for the
// `go tool cover -func` error path.
type errCovRunner struct{ out string }

func (e *errCovRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(e.out))
	}
	return errors.New("exit status 1")
}

func (*errCovRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// TestParseMergedBlocksMalformed covers every reject branch of the
// coverprofile row parser. A malformed row must be skipped rather
// than panicking or contributing a half-built block, because the
// merged profile is concatenated from N per-module files and one
// truncated write would otherwise poison the whole aggregate.
func TestParseMergedBlocksMalformed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
	}{
		{"too few fields", "a.go:1.1,2.2 1"},
		{"head without a colon", "noColonToken 1 1"},
		{"non-numeric statement count", "a.go:1.1,2.2 x 1"},
		{"non-numeric hit count", "a.go:1.1,2.2 1 x"},
		{"span with no comma", "a.go:1.1 1 1"},
		{"non-numeric start line", "a.go:x.1,2.2 1 1"},
		{"non-numeric end line", "a.go:1.1,x.2 1 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseMergedBlocks("mode: atomic\n" + tc.line + "\n"); len(got) != 0 {
				t.Errorf("parseMergedBlocks(%q) = %+v, want the row rejected", tc.line, got)
			}
		})
	}

	t.Run("repeated span sums the counts", func(t *testing.T) {
		t.Parallel()
		// The dedup branch: the same block appearing in two module
		// profiles must sum, matching what `go tool cover` derives.
		got := parseMergedBlocks("mode: atomic\n" +
			"a.go:1.1,2.2 3 4\n" +
			"a.go:1.1,2.2 3 6\n")
		if len(got) != 1 {
			t.Fatalf("blocks = %+v, want one deduplicated block", got)
		}
		if got[0].Count != 10 {
			t.Errorf("Count = %d, want the two rows summed to 10", got[0].Count)
		}
		if got[0].StartLine != 1 || got[0].EndLine != 2 {
			t.Errorf("span = %d-%d, want 1-2", got[0].StartLine, got[0].EndLine)
		}
	})
}

// TestParseFuncLogMalformed covers the reject branches of the
// `go tool cover -func` parser.
func TestParseFuncLogMalformed(t *testing.T) {
	t.Parallel()

	out := strings.Join([]string{
		"short line",                  // < 3 fields
		"total:\t(statements)\t91.4%", // summary row is dropped
		"a.go:1:\tA\tnotapercent",     // unparseable percentage
		"a.go:2:\tB\t80.0%",           // the only valid row
	}, "\n")
	rows := parseFuncLog(out)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want only the well-formed row", rows)
	}
	if rows[0].Func != "B" || rows[0].Pct != 80 {
		t.Errorf("row = %+v, want B at 80%%", rows[0])
	}
}

// TestFindProfiles covers the directory scan: a missing directory
// is not an error (the user simply has not run `ergon test`), and
// non-`.out` entries plus subdirectories are ignored.
func TestFindProfiles(t *testing.T) {
	t.Parallel()

	t.Run("missing directory yields no profiles and no error", func(t *testing.T) {
		t.Parallel()
		got, err := findProfiles(filepath.Join(t.TempDir(), "absent"))
		if err != nil {
			t.Fatalf("findProfiles err = %v, want nil for a missing dir", err)
		}
		if len(got) != 0 {
			t.Errorf("profiles = %v, want none", got)
		}
	})

	t.Run("ignores subdirectories and non-.out files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "nested.out"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for _, name := range []string{"a.out", "b.out", "notes.txt", "c.html"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		got, err := findProfiles(dir)
		if err != nil {
			t.Fatalf("findProfiles: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("profiles = %v, want only the two .out files", got)
		}
		if filepath.Base(got[0]) != "a.out" || filepath.Base(got[1]) != "b.out" {
			t.Errorf("profiles = %v, want them sorted by name", got)
		}
	})
}

// shortWriter accepts limit bytes and then reports the write as
// short, which is what a filesystem does when it runs out of space
// partway through a write(2).
type shortWriter struct {
	limit int
	got   []byte
	err   error

	// quiet reports a short write with a nil error, which
	// [io.Writer] permits and os.File does not do. The count check
	// exists for writers that behave this way, so it needs a writer
	// that behaves this way to be exercised at all.
	quiet bool
}

func (s *shortWriter) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n := min(len(p), s.limit)
	s.got = append(s.got, p[:n]...)
	if n != len(p) && !s.quiet {
		// os.File.Write converts a partial write into this error;
		// mirroring it keeps the fake honest to the real seam.
		return n, io.ErrShortWrite
	}
	return n, nil
}

// TestWriteMergedProfile pins the contract that stops a truncated
// merge from reaching `go tool cover` as data.
//
// The merge used to call fmt.Fprintln per line and discard both
// return values. A write that stopped partway therefore lost the
// tail of one record, and the next line's bytes landed against it
// — producing a single malformed record spanning two modules:
//
//	...ttl.go:72.2,72.12 go.thesmos.sh/testkit/core/trace
//
// go tool cover rejected that as a malformed import path, which was
// the good case. A truncation that still parsed would have yielded
// a silently wrong percentage instead.
func TestWriteMergedProfile(t *testing.T) {
	t.Parallel()

	const body = "mode: atomic\nx.go:1.1,2.2 1 1\ny.go:1.1,2.2 1 0\n"

	t.Run("a short write is an error, not a truncation", func(t *testing.T) {
		t.Parallel()
		w := &shortWriter{limit: 20}
		err := writeMergedProfile(w, body)
		if err == nil {
			t.Fatal("writeMergedProfile = nil, want the short write reported")
		}
		if len(w.got) >= len(body) {
			t.Fatalf("wrote %d bytes, want the fake to have truncated", len(w.got))
		}
	})

	t.Run("a failing write propagates", func(t *testing.T) {
		t.Parallel()
		err := writeMergedProfile(&shortWriter{err: errors.New("no space left on device")}, body)
		if err == nil {
			t.Fatal("writeMergedProfile = nil, want the failure surfaced")
		}
		if !strings.Contains(err.Error(), "no space left") {
			t.Errorf("err = %v, want the underlying cause named", err)
		}
	})

	t.Run("a silent short write is caught by the count", func(t *testing.T) {
		t.Parallel()
		// io.Writer allows (n < len(p), nil). Nothing would report
		// the shortfall if the count were not checked alongside the
		// error, and the merged profile would be truncated exactly
		// as it was before.
		err := writeMergedProfile(&shortWriter{limit: 20, quiet: true}, body)
		if err == nil {
			t.Fatal("writeMergedProfile = nil, want the shortfall caught by the byte count")
		}
		if !strings.Contains(err.Error(), "of "+strconv.Itoa(len(body))+" bytes") {
			t.Errorf("err = %v, want it to report how much was written", err)
		}
	})

	t.Run("a complete write succeeds and writes every byte", func(t *testing.T) {
		t.Parallel()
		w := &shortWriter{limit: len(body)}
		if err := writeMergedProfile(w, body); err != nil {
			t.Fatalf("writeMergedProfile: %v", err)
		}
		if string(w.got) != body {
			t.Errorf("wrote %q, want %q", w.got, body)
		}
	})
}

// TestMergeProfilesFileMatchesBody pins the invariant the caller
// depends on: the bytes handed to `go tool cover` and the string
// handed to aggregateByLayer are the same data.
//
// They used to be maintained in parallel — one Fprintln to the
// file, one WriteString to a strings.Builder that cannot fail. A
// failed file write left the two disagreeing, so the layer
// percentages were computed from records the tool never saw.
func TestMergeProfilesFileMatchesBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.out")
	b := filepath.Join(dir, "b.out")
	if err := os.WriteFile(a, []byte("mode: atomic\nx.go:1.1,2.2 1 1\n\n"), 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(b, []byte("mode: atomic\ny.go:1.1,2.2 1 0\n"), 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}

	path, body, cleanup, err := mergeProfiles([]string{a, b})
	if err != nil {
		t.Fatalf("mergeProfiles: %v", err)
	}
	defer cleanup()

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read merged: %v", err)
	}
	if string(onDisk) != body {
		t.Errorf("file = %q, body = %q, want identical", onDisk, body)
	}
	// Every record must end in a newline; the reported corruption
	// was two records sharing a line.
	for line := range strings.SplitSeq(strings.TrimSuffix(string(onDisk), "\n"), "\n") {
		if strings.Count(line, ".go:") > 1 {
			t.Errorf("line = %q, want at most one record per line", line)
		}
	}
}

// TestMergeProfiles covers the concatenation: exactly one `mode:`
// header survives across N inputs, blank lines are dropped, and an
// unreadable input aborts with the path named.
func TestMergeProfiles(t *testing.T) {
	t.Parallel()

	t.Run("keeps one mode header across several profiles", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := filepath.Join(dir, "a.out")
		b := filepath.Join(dir, "b.out")
		if err := os.WriteFile(a, []byte("mode: atomic\nx.go:1.1,2.2 1 1\n\n"), 0o600); err != nil {
			t.Fatalf("write a: %v", err)
		}
		if err := os.WriteFile(b, []byte("mode: atomic\ny.go:1.1,2.2 1 2\n"), 0o600); err != nil {
			t.Fatalf("write b: %v", err)
		}

		path, body, cleanup, err := mergeProfiles([]string{a, b})
		if err != nil {
			t.Fatalf("mergeProfiles: %v", err)
		}
		defer cleanup()

		if got := strings.Count(body, "mode:"); got != 1 {
			t.Errorf("body has %d mode headers, want exactly one", got)
		}
		if !strings.Contains(body, "x.go") || !strings.Contains(body, "y.go") {
			t.Errorf("body = %q, want rows from both profiles", body)
		}
		if strings.Contains(body, "\n\n") {
			t.Errorf("body = %q, want blank lines dropped", body)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("merged file not written: %v", statErr)
		}
	})

	t.Run("an unreadable profile aborts and names the path", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "gone.out")
		_, _, cleanup, err := mergeProfiles([]string{missing})
		defer cleanup()
		if err == nil {
			t.Fatal("mergeProfiles returned nil, want the read failure")
		}
		if !strings.Contains(err.Error(), "gone.out") {
			t.Errorf("err = %v, want it to name the unreadable profile", err)
		}
	})
}

// TestLongestPrefixLayerIdxOrdering covers the tie-break: a later,
// shorter declaration must not displace an earlier, longer match.
func TestLongestPrefixLayerIdxOrdering(t *testing.T) {
	t.Parallel()

	// The specific layer is declared FIRST, so the loop sees the
	// shorter `internal/...` afterwards and must reject it.
	packages := []Layer{
		{Path: "internal/checks/...", Line: 80},
		{Path: "internal/...", Line: 70},
	}
	if got := LongestPrefixLayerIdx(packages, "internal/checks/coverage"); got != 0 {
		t.Errorf("idx = %d, want 0 (the more specific layer)", got)
	}
	if got := LongestPrefixLayerIdx(packages, "internal/style"); got != 1 {
		t.Errorf("idx = %d, want 1 (the general layer)", got)
	}
	if got := LongestPrefixLayerIdx(packages, "cmd/ergon"); got != -1 {
		t.Errorf("idx = %d, want -1 for an unclaimed path", got)
	}
}

// TestSelectTargetsEdges covers the branches the happy-path test
// does not reach: the workspace wildcard is not selectable by name,
// an unmatched request is dropped, and the longest declared prefix
// wins regardless of declaration order.
func TestSelectTargetsEdges(t *testing.T) {
	t.Parallel()

	t.Run("the wildcard sentinel is not addressable", func(t *testing.T) {
		t.Parallel()
		// A typo must not silently inherit the workspace-wide
		// threshold; it selects nothing so Run reports a usage error.
		packages := []Layer{{Path: "./...", Line: 70}}
		got, idxs := SelectTargets(packages, []string{"typoo"})
		if len(got) != 0 || len(idxs) != 0 {
			t.Errorf("targets = %+v, want none", got)
		}
	})

	t.Run("a more specific later declaration wins", func(t *testing.T) {
		t.Parallel()
		packages := []Layer{
			{Path: "internal/...", Line: 70},
			{Path: "internal/checks/...", Line: 80},
		}
		got, idxs := SelectTargets(packages, []string{"internal/checks"})
		if len(got) != 1 {
			t.Fatalf("targets = %+v, want one", got)
		}
		if idxs[0] != 1 || got[0].Line != 80 {
			t.Errorf("target = %+v (idx %d), want the internal/checks entry", got[0], idxs[0])
		}
	})

	t.Run("an earlier longer declaration is not displaced", func(t *testing.T) {
		t.Parallel()
		packages := []Layer{
			{Path: "internal/checks/...", Line: 80},
			{Path: "internal/...", Line: 70},
		}
		_, idxs := SelectTargets(packages, []string{"internal/checks/coverage"})
		if len(idxs) != 1 || idxs[0] != 0 {
			t.Errorf("idxs = %v, want [0]", idxs)
		}
	})

	t.Run("a trailing slash is tolerated", func(t *testing.T) {
		t.Parallel()
		packages := []Layer{{Path: "internal/...", Line: 70}}
		if got, _ := SelectTargets(packages, []string{"internal/"}); len(got) != 1 {
			t.Errorf("targets = %+v, want the trailing slash trimmed", got)
		}
	})
}

// TestRunErrorPaths covers Run's failure branches, each of which
// aborts before any verdict is rendered.
func TestRunErrorPaths(t *testing.T) {
	t.Parallel()

	cfg := Config{Packages: []Layer{{Path: "internal/...", Line: 70}}}

	t.Run("no profiles tells the user to run ergon test", func(t *testing.T) {
		t.Parallel()
		err := Run(t.Context(), &fakeCovRunner{}, io.Discard, io.Discard,
			"", t.TempDir(), testImports, cfg, nil, nil, RunOptions{})
		if err == nil {
			t.Fatal("Run returned nil, want the no-profiles error")
		}
		if !strings.Contains(err.Error(), "ergon test") {
			t.Errorf("err = %v, want it to name the remedy", err)
		}
	})

	t.Run("a go tool cover failure propagates", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.out"),
			[]byte("mode: atomic\nx.go:1.1,2.2 1 1\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		err := Run(t.Context(), &errCovRunner{out: "cover: cannot open"}, io.Discard, io.Discard,
			"", dir, testImports, cfg, nil, nil, RunOptions{})
		if err == nil {
			t.Fatal("Run returned nil, want the go tool cover failure")
		}
		if !strings.Contains(err.Error(), "go tool cover") {
			t.Errorf("err = %v, want it to name the failing step", err)
		}
	})

	t.Run("an unmatched target is a usage error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.out"),
			[]byte("mode: atomic\ngo.example.com/x/internal/a.go:1.1,2.2 1 1\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		runner := &fakeCovRunner{funcLog: "go.example.com/x/internal/a.go:1:\tA\t100.0%\n"}
		err := Run(t.Context(), runner, io.Discard, io.Discard,
			"", dir, testImports, cfg, nil, nil,
			RunOptions{Targets: []string{"nosuchlayer"}})
		if err == nil {
			t.Fatal("Run returned nil, want the no-matching-targets error")
		}
	})
}

// TestLayerStatsPct pins the empty-layer sentinel: a layer with no
// claimed statements reports 0 rather than dividing by zero, so it
// can never fail the gate by accident.
func TestLayerStatsPct(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   layerStats
		want float64
	}{
		{layerStats{}, 0},
		{layerStats{TotalStmts: 0, CoveredStmts: 5}, 0},
		{layerStats{TotalStmts: 4, CoveredStmts: 1}, 25},
		{layerStats{TotalStmts: 4, CoveredStmts: 4}, 100},
	}
	for _, tc := range cases {
		if got := tc.in.Pct(); got != tc.want {
			t.Errorf("Pct(%+v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestAggregateByLayerPolicy covers the three ways a coverprofile
// block is dropped before it reaches a layer's totals: no declared
// layer claims it, an exclude matches its path, or a structural
// skip matches its function.
func TestAggregateByLayerPolicy(t *testing.T) {
	t.Parallel()

	packages := []Layer{{Path: "internal/...", Line: 70}}
	merged := "mode: atomic\n" +
		"go.example.com/x/internal/a.go:1.1,2.2 5 1\n" + // counted
		"go.example.com/x/cmd/main.go:1.1,2.2 7 1\n" + // no layer claims it
		"go.example.com/x/internal/a_test.go:1.1,2.2 9 1\n" // excluded

	t.Run("unclaimed and excluded blocks are dropped", func(t *testing.T) {
		t.Parallel()
		excludes := []policy.Exclude{{Path: "**/*_test.go", Reason: "tests"}}
		got := aggregateByLayer(merged, packages, testImports, excludes, nil, nil)
		if len(got) != 1 {
			t.Fatalf("stats = %+v, want one layer", got)
		}
		if got[0].TotalStmts != 5 {
			t.Errorf("TotalStmts = %d, want only the 5 claimed statements", got[0].TotalStmts)
		}
	})

	t.Run("a structural skip drops the block", func(t *testing.T) {
		t.Parallel()
		// The skip needs the function name, which comes from the
		// funcSpans index; span 1-2 in a.go belongs to A.
		spans := map[string][]funcSpan{
			"internal/a.go": {{Func: "A", StartLine: 1}},
		}
		skips := []policy.Skip{{
			Label: "assertions", FuncGlob: "A", FileGlob: "internal/*.go",
		}}
		got := aggregateByLayer(
			"mode: atomic\ngo.example.com/x/internal/a.go:1.1,2.2 5 1\n",
			packages, testImports, nil, skips, spans,
		)
		if got[0].TotalStmts != 0 {
			t.Errorf("TotalStmts = %d, want the skipped function dropped", got[0].TotalStmts)
		}
	})

	t.Run("uncovered blocks count toward the total but not the covered", func(t *testing.T) {
		t.Parallel()
		got := aggregateByLayer("mode: atomic\n"+
			"go.example.com/x/internal/a.go:1.1,2.2 3 1\n"+
			"go.example.com/x/internal/b.go:1.1,2.2 7 0\n",
			packages, testImports, nil, nil, nil)
		if got[0].TotalStmts != 10 || got[0].CoveredStmts != 3 {
			t.Errorf("stats = %+v, want 3/10", got[0])
		}
	})
}

// TestRenderTargetFailureList covers the failing-target diagnostic
// block: the list is capped at topN with a "… and N more" tail, and
// verbose appends the uncovered ranges.
func TestRenderTargetFailureList(t *testing.T) {
	t.Parallel()

	layer := Layer{Path: "internal/...", Line: 90}
	// Six failing functions against a cap of two.
	var rows []funcRow
	var rowClaim []int
	var merged strings.Builder
	merged.WriteString("mode: atomic\n")
	for i := range 6 {
		rows = append(rows, funcRow{
			Path: fmt.Sprintf("internal/f%d.go", i),
			Func: fmt.Sprintf("F%d", i),
			Pct:  float64(i * 10),
		})
		rowClaim = append(rowClaim, 0)
		fmt.Fprintf(&merged, "go.example.com/x/internal/f%d.go:%d.1,%d.2 1 0\n", i, i+1, i+2)
	}
	agg := layerStats{TotalStmts: 10, CoveredStmts: 1}

	t.Run("caps the list and reports the remainder", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		failed := renderTarget(&out, style.Style{}, layer, 0, rows, rowClaim, agg,
			nil, nil, testImports, 2, false, merged.String())
		if !failed {
			t.Fatal("renderTarget returned false, want the layer to fail")
		}
		if !strings.Contains(out.String(), "and 4 more function(s)") {
			t.Errorf("output = %q, want the capped remainder reported", out.String())
		}
	})

	t.Run("verbose appends the uncovered ranges", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		renderTarget(&out, style.Style{}, layer, 0, rows, rowClaim, agg,
			nil, nil, testImports, 10, true, merged.String())
		if !strings.Contains(out.String(), "Uncovered ranges") {
			t.Errorf("output = %q, want the verbose range dump", out.String())
		}
	})

	t.Run("a passing layer renders no failure list", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		failed := renderTarget(&out, style.Style{}, Layer{Path: "internal/...", Line: 10},
			0, rows, rowClaim, layerStats{TotalStmts: 10, CoveredStmts: 9},
			nil, nil, testImports, 10, false, merged.String())
		if failed {
			t.Fatal("renderTarget returned true, want the layer to pass")
		}
		if strings.Contains(out.String(), "Lowest-coverage functions") {
			t.Errorf("output = %q, want no failure list on a pass", out.String())
		}
	})
}

// TestWriteUncoveredRangesFilters covers the two skip branches: no
// failing files at all, and blocks that were actually executed.
func TestWriteUncoveredRangesFilters(t *testing.T) {
	t.Parallel()

	t.Run("no failing files writes nothing", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		writeUncoveredRanges(&out, style.Style{}, nil, testImports,
			"mode: atomic\ngo.example.com/x/internal/a.go:1.1,2.2 1 0\n")
		if out.String() != "" {
			t.Errorf("output = %q, want nothing", out.String())
		}
	})

	t.Run("covered blocks are omitted", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		writeUncoveredRanges(&out, style.Style{},
			[]Failure{{Path: "internal/a.go", Func: "A"}}, testImports,
			"mode: atomic\n"+
				"go.example.com/x/internal/a.go:1.1,2.2 1 5\n"+ // executed
				"go.example.com/x/internal/a.go:9.1,10.2 3 0\n") // not
		if strings.Contains(out.String(), "1-2") {
			t.Errorf("output = %q, want the executed block omitted", out.String())
		}
		if !strings.Contains(out.String(), "9-10") {
			t.Errorf("output = %q, want the uncovered block listed", out.String())
		}
	})
}
