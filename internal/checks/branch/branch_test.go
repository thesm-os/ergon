// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package branch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/checks/policy"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
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

// fakeRunner satisfies [xexec.Runner] for the shell-out tests. It
// dispatches on the command name: `go list` writes listOutput,
// `gobco` writes the JSON in statsByDir (keyed by the package dir
// it is invoked from) to the -stats path and records the call.
type fakeRunner struct {
	mu sync.Mutex

	listOutput string
	listErr    error

	// statsByDir maps a package directory to the gobco JSON that
	// run should write for it. A missing entry writes nothing,
	// which is how gobco behaves for a package with no conditions.
	statsByDir map[string]string
	gobcoOut   string
	gobcoErr   error

	listCalls  int
	gobcoCalls int
	gobcoDirs  []string
}

func (f *fakeRunner) Run(
	_ context.Context, opts xexec.Options, name string, args ...string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if name == "go" {
		f.listCalls++
		if opts.Stdout != nil {
			_, _ = opts.Stdout.Write([]byte(f.listOutput))
		}
		return f.listErr
	}

	f.gobcoCalls++
	f.gobcoDirs = append(f.gobcoDirs, filepath.ToSlash(opts.Dir))
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(f.gobcoOut))
	}
	if f.gobcoErr != nil {
		return f.gobcoErr
	}
	// Mirror gobco: write the stats file at the -stats path.
	statsPath := ""
	for i, a := range args {
		if a == "-stats" && i+1 < len(args) {
			statsPath = args[i+1]
		}
	}
	if body, ok := f.statsByDir[filepath.ToSlash(opts.Dir)]; ok && statsPath != "" {
		if err := os.WriteFile(statsPath, []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// TestListWorkspacePackages covers the `go list` fan-out that maps
// every workspace package to its import path, absolute dir, and
// repo-relative dir.
func TestListWorkspacePackages(t *testing.T) {
	t.Parallel()

	t.Run("parses one line per package", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{listOutput: "" +
			"example.test/proj/a " + filepath.Join(root, "a") + "\n" +
			"example.test/proj/b/c " + filepath.Join(root, "b", "c") + "\n"}

		got, err := listWorkspacePackages(t.Context(), runner, root, []modules.Import{{Dir: "."}})
		if err != nil {
			t.Fatalf("listWorkspacePackages: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("packages = %+v, want two", got)
		}
		if got[0].ImportPath != "example.test/proj/a" || got[0].RepoRel != "a" {
			t.Errorf("first = %+v, want import example.test/proj/a and repo-rel a", got[0])
		}
		if got[1].RepoRel != "b/c" {
			t.Errorf("second RepoRel = %q, want b/c", got[1].RepoRel)
		}
	})

	t.Run("skips blank and malformed lines", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		// A line with no dir field cannot be mapped to a package and
		// must not produce a half-populated record.
		runner := &fakeRunner{listOutput: "\n   \nlonelyimportpath\nexample.test/x " +
			filepath.Join(root, "x") + "\n"}

		got, err := listWorkspacePackages(t.Context(), runner, root, []modules.Import{{Dir: "."}})
		if err != nil {
			t.Fatalf("listWorkspacePackages: %v", err)
		}
		if len(got) != 1 || got[0].ImportPath != "example.test/x" {
			t.Fatalf("packages = %+v, want only the well-formed entry", got)
		}
	})

	t.Run("runs once per module and unions the results", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		runner := &fakeRunner{listOutput: "example.test/m " + filepath.Join(root, "m") + "\n"}

		got, err := listWorkspacePackages(t.Context(), runner, root,
			[]modules.Import{{Dir: "."}, {Dir: "sub"}})
		if err != nil {
			t.Fatalf("listWorkspacePackages: %v", err)
		}
		if runner.listCalls != 2 {
			t.Errorf("go list ran %d times, want one per module", runner.listCalls)
		}
		if len(got) != 2 {
			t.Errorf("packages = %+v, want the union across both modules", got)
		}
	})

	t.Run("a go list failure names the module and carries the output", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			listOutput: "build constraints exclude all Go files",
			listErr:    errors.New("exit status 1"),
		}
		_, err := listWorkspacePackages(t.Context(), runner, t.TempDir(),
			[]modules.Import{{Dir: "backend"}})
		if err == nil {
			t.Fatal("listWorkspacePackages returned nil, want the go list error")
		}
		if !strings.Contains(err.Error(), "backend") {
			t.Errorf("err = %v, want it to name the failing module", err)
		}
		if !strings.Contains(err.Error(), "build constraints") {
			t.Errorf("err = %v, want the tool output attached", err)
		}
	})
}

// TestRunGobcoOne covers the per-package gobco invocation and the
// three shapes its result can take: parsed records, the benign
// "nothing to instrument" exit, and a real failure.
func TestRunGobcoOne(t *testing.T) {
	t.Parallel()

	const stats = `[{"Start":"a.go:4:5","Code":"x > 0","TrueCount":1,"FalseCount":2}]`

	t.Run("decodes the stats file gobco writes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runner := &fakeRunner{statsByDir: map[string]string{filepath.ToSlash(dir): stats}}

		recs, err := runGobcoOne(t.Context(), runner, "", pkgInfo{Dir: dir},
			filepath.Join(t.TempDir(), "s.json"))
		if err != nil {
			t.Fatalf("runGobcoOne: %v", err)
		}
		if len(recs) != 1 || recs[0].Start != "a.go:4:5" {
			t.Fatalf("records = %+v, want the single decoded condition", recs)
		}
		if recs[0].TrueCount != 1 || recs[0].FalseCount != 2 {
			t.Errorf("counts = (%d, %d), want (1, 2)", recs[0].TrueCount, recs[0].FalseCount)
		}
		if runner.gobcoDirs[0] != filepath.ToSlash(dir) {
			t.Errorf("gobco ran in %q, want the package dir %q", runner.gobcoDirs[0], dir)
		}
	})

	t.Run("nothing to instrument is not an error", func(t *testing.T) {
		t.Parallel()
		// gobco exits non-zero for a package with no conditionals;
		// the layer simply contributes zero conditions.
		runner := &fakeRunner{
			gobcoOut: "gobco: nothing to instrument\n",
			gobcoErr: errors.New("exit status 1"),
		}
		recs, err := runGobcoOne(t.Context(), runner, "", pkgInfo{Dir: t.TempDir()},
			filepath.Join(t.TempDir(), "s.json"))
		if err != nil {
			t.Fatalf("runGobcoOne err = %v, want nil for the benign exit", err)
		}
		if len(recs) != 0 {
			t.Errorf("records = %+v, want none", recs)
		}
	})

	t.Run("a missing stats file yields no records", func(t *testing.T) {
		t.Parallel()
		// gobco skips writing the file when it found nothing to do.
		runner := &fakeRunner{}
		recs, err := runGobcoOne(t.Context(), runner, "", pkgInfo{Dir: t.TempDir()},
			filepath.Join(t.TempDir(), "absent.json"))
		if err != nil {
			t.Fatalf("runGobcoOne err = %v, want nil", err)
		}
		if len(recs) != 0 {
			t.Errorf("records = %+v, want none", recs)
		}
	})

	t.Run("a real gobco failure surfaces with its output", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			gobcoOut: "cannot load package",
			gobcoErr: errors.New("exit status 2"),
		}
		_, err := runGobcoOne(t.Context(), runner, "", pkgInfo{Dir: t.TempDir()},
			filepath.Join(t.TempDir(), "s.json"))
		if err == nil {
			t.Fatal("runGobcoOne returned nil, want the gobco failure")
		}
		if !strings.Contains(err.Error(), "cannot load package") {
			t.Errorf("err = %v, want the tool output attached", err)
		}
	})

	t.Run("malformed stats JSON surfaces as a parse error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runner := &fakeRunner{statsByDir: map[string]string{filepath.ToSlash(dir): "{not json"}}
		_, err := runGobcoOne(t.Context(), runner, "", pkgInfo{Dir: dir},
			filepath.Join(t.TempDir(), "s.json"))
		if err == nil {
			t.Fatal("runGobcoOne returned nil, want a JSON parse error")
		}
	})
}

// TestRunGobcoAllPackages covers the bounded worker pool: every
// package is visited, results land in declaration order, and a
// single failure surfaces without losing the other results.
func TestRunGobcoAllPackages(t *testing.T) {
	t.Parallel()

	t.Run("visits every package and preserves order", func(t *testing.T) {
		t.Parallel()
		pkgs := make([]pkgInfo, 0, 8)
		statsByDir := map[string]string{}
		for i := range 8 {
			dir := t.TempDir()
			pkgs = append(pkgs, pkgInfo{ImportPath: fmt.Sprintf("p%d", i), Dir: dir})
			statsByDir[filepath.ToSlash(dir)] = fmt.Sprintf(
				`[{"Start":"f%d.go:1:1","Code":"c","TrueCount":1,"FalseCount":1}]`, i)
		}
		runner := &fakeRunner{statsByDir: statsByDir}

		got, err := runGobcoAllPackages(t.Context(), runner, io.Discard, t.TempDir(), pkgs, nil, 3)
		if err != nil {
			t.Fatalf("runGobcoAllPackages: %v", err)
		}
		if len(got) != len(pkgs) {
			t.Fatalf("stats = %d entries, want %d", len(got), len(pkgs))
		}
		for i := range pkgs {
			if got[i].Pkg.ImportPath != fmt.Sprintf("p%d", i) {
				t.Errorf("index %d holds %q, want the input order preserved",
					i, got[i].Pkg.ImportPath)
			}
			if len(got[i].Records) != 1 {
				t.Errorf("index %d has %d records, want one", i, len(got[i].Records))
			}
		}
		if runner.gobcoCalls != len(pkgs) {
			t.Errorf("gobco ran %d times, want once per package", runner.gobcoCalls)
		}
	})

	t.Run("zero workers falls back to a sane default", func(t *testing.T) {
		t.Parallel()
		pkgs := []pkgInfo{{ImportPath: "a", Dir: t.TempDir()}}
		runner := &fakeRunner{}
		if _, err := runGobcoAllPackages(
			t.Context(), runner, io.Discard, t.TempDir(), pkgs, nil, 0); err != nil {
			t.Fatalf("runGobcoAllPackages: %v", err)
		}
		if runner.gobcoCalls != 1 {
			t.Errorf("gobco ran %d times, want one", runner.gobcoCalls)
		}
	})

	t.Run("no packages is not an error", func(t *testing.T) {
		t.Parallel()
		got, err := runGobcoAllPackages(
			t.Context(), &fakeRunner{}, io.Discard, t.TempDir(), nil, nil, 2)
		if err != nil {
			t.Fatalf("runGobcoAllPackages err = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Errorf("stats = %+v, want empty", got)
		}
	})

	t.Run("a failing package surfaces the error and reports it", func(t *testing.T) {
		t.Parallel()
		pkgs := []pkgInfo{{ImportPath: "boom", Dir: t.TempDir()}}
		runner := &fakeRunner{gobcoOut: "boom", gobcoErr: errors.New("exit status 2")}

		var stderr bytes.Buffer
		_, err := runGobcoAllPackages(t.Context(), runner, &stderr, t.TempDir(), pkgs, nil, 2)
		if err == nil {
			t.Fatal("runGobcoAllPackages returned nil, want the gobco failure")
		}
		if !strings.Contains(stderr.String(), "boom") {
			t.Errorf("stderr = %q, want the failing package named", stderr.String())
		}
	})
}

// TestRun covers the `ergon check branch` entry point end to end
// against a fake runner: the no-thresholds short-circuit, a passing
// layer, a failing required layer, and error propagation.
func TestRun(t *testing.T) {
	t.Parallel()

	// setup builds a workspace whose single package carries one
	// fully-covered condition and one half-covered condition, i.e.
	// 50% branch coverage.
	setup := func(t *testing.T) (root string, runner *fakeRunner) {
		t.Helper()
		root = t.TempDir()
		pkgDir := filepath.Join(root, "internal", "a")
		if err := os.MkdirAll(pkgDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		return root, &fakeRunner{
			listOutput: "example.test/proj/internal/a " + pkgDir + "\n",
			statsByDir: map[string]string{
				filepath.ToSlash(pkgDir): `[
					{"Start":"x.go:1:1","Code":"a","TrueCount":1,"FalseCount":1},
					{"Start":"x.go:2:1","Code":"b","TrueCount":1,"FalseCount":0}
				]`,
			},
		}
	}

	t.Run("no declared thresholds short-circuits with a notice", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		err := Run(t.Context(), &fakeRunner{}, &stdout, io.Discard,
			t.TempDir(), nil, coverage.Config{}, nil, nil, RunOptions{})
		if err != nil {
			t.Fatalf("Run err = %v, want nil", err)
		}
		if !strings.Contains(stdout.String(), "no thresholds declared") {
			t.Errorf("stdout = %q, want the skip notice", stdout.String())
		}
	})

	t.Run("a layer at or above its threshold passes", func(t *testing.T) {
		t.Parallel()
		root, runner := setup(t)
		cfg := coverage.Config{Packages: []coverage.Layer{
			{Path: "internal/...", Branch: 50, RequireBranch: true},
		}}
		var stdout bytes.Buffer
		if err := Run(t.Context(), runner, &stdout, io.Discard, root,
			[]modules.Import{{Dir: "."}}, cfg, nil, nil, RunOptions{}); err != nil {
			t.Fatalf("Run err = %v, want nil at exactly the threshold", err)
		}
		if !strings.Contains(stdout.String(), "50.0%") {
			t.Errorf("stdout = %q, want the measured percentage", stdout.String())
		}
	})

	t.Run("a required layer below threshold fails the run", func(t *testing.T) {
		t.Parallel()
		root, runner := setup(t)
		cfg := coverage.Config{Packages: []coverage.Layer{
			{Path: "internal/...", Branch: 80, RequireBranch: true},
		}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root,
			[]modules.Import{{Dir: "."}}, cfg, nil, nil, RunOptions{})
		if err == nil {
			t.Fatal("Run returned nil, want the below-threshold failure")
		}
	})

	t.Run("an informational layer below threshold does not fail", func(t *testing.T) {
		t.Parallel()
		root, runner := setup(t)
		cfg := coverage.Config{Packages: []coverage.Layer{
			{Path: "internal/...", Branch: 80, RequireBranch: false},
		}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, root,
			[]modules.Import{{Dir: "."}}, cfg, nil, nil, RunOptions{}); err != nil {
			t.Fatalf("Run err = %v, want nil for an informational layer", err)
		}
	})

	t.Run("a go list failure propagates", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{listErr: errors.New("exit status 1")}
		cfg := coverage.Config{Packages: []coverage.Layer{{Path: "internal/...", Branch: 50}}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, t.TempDir(),
			[]modules.Import{{Dir: "."}}, cfg, nil, nil, RunOptions{}); err == nil {
			t.Fatal("Run returned nil, want the go list failure")
		}
	})

	t.Run("an unmatched target is a usage error", func(t *testing.T) {
		t.Parallel()
		root, runner := setup(t)
		cfg := coverage.Config{Packages: []coverage.Layer{{Path: "internal/...", Branch: 50}}}
		err := Run(t.Context(), runner, io.Discard, io.Discard, root,
			[]modules.Import{{Dir: "."}}, cfg, nil, nil,
			RunOptions{Targets: []string{"nosuchlayer"}})
		if err == nil {
			t.Fatal("Run returned nil, want the no-matching-targets error")
		}
	})
}

// wsImports is a two-module workspace: the root plus `alpha`.
var wsImports = []modules.Import{
	{Dir: ".", ImportPath: "example.test/ws"},
	{Dir: "alpha", ImportPath: "example.test/ws/alpha"},
}

// TestCoupledModule pins the detection rule for the one layout
// gobco structurally cannot measure.
func TestCoupledModule(t *testing.T) {
	t.Parallel()

	t.Run("a cross-module import is coupled", func(t *testing.T) {
		t.Parallel()
		pkg := pkgInfo{
			ImportPath: "example.test/ws/beta", RepoRel: "beta",
			Imports: []string{"fmt", "example.test/ws/alpha"},
		}
		dep, ok := coupledModule(pkg, wsImports)
		if !ok || dep != "example.test/ws/alpha" {
			t.Fatalf("coupledModule = (%q, %v), want the alpha module", dep, ok)
		}
	})

	t.Run("a subpackage of another module counts", func(t *testing.T) {
		t.Parallel()
		pkg := pkgInfo{
			ImportPath: "example.test/ws/beta", RepoRel: "beta",
			Imports: []string{"example.test/ws/alpha/deep"},
		}
		if _, ok := coupledModule(pkg, wsImports); !ok {
			t.Error("coupledModule = false, want the nested import to couple")
		}
	})

	t.Run("importing only its own module is not coupled", func(t *testing.T) {
		t.Parallel()
		// A package inside alpha importing another alpha package.
		pkg := pkgInfo{
			ImportPath: "example.test/ws/alpha/sub", RepoRel: "alpha/sub",
			Imports: []string{"example.test/ws/alpha/other", "strings"},
		}
		if dep, ok := coupledModule(pkg, wsImports); ok {
			t.Errorf("coupledModule = %q, want no coupling within one module", dep)
		}
	})

	t.Run("nested module paths do not confuse ownership", func(t *testing.T) {
		t.Parallel()
		// alpha's import path is prefixed by the root module's, so an
		// import-path prefix test would wrongly call the root "its
		// own module" and miss the coupling. Ownership is by
		// directory, so this must still be detected.
		pkg := pkgInfo{
			ImportPath: "example.test/ws/alpha", RepoRel: "alpha",
			Imports: []string{"example.test/ws/shared"},
		}
		dep, ok := coupledModule(pkg, wsImports)
		if !ok || dep != "example.test/ws" {
			t.Fatalf("coupledModule = (%q, %v), want the root module", dep, ok)
		}
	})

	t.Run("a single-module repo never couples", func(t *testing.T) {
		t.Parallel()
		single := []modules.Import{{Dir: ".", ImportPath: "example.test/solo"}}
		pkg := pkgInfo{
			ImportPath: "example.test/solo/internal/a", RepoRel: "internal/a",
			Imports: []string{"example.test/solo/internal/b", "fmt"},
		}
		if dep, ok := coupledModule(pkg, single); ok {
			t.Errorf("coupledModule = %q, want none in a single-module repo", dep)
		}
	})
}

// TestWorkspaceCoupledPackagesSkip covers the demotion: a gobco
// failure on a workspace-coupled package becomes a reported skip,
// while every other failure still fails the run.
func TestWorkspaceCoupledPackagesSkip(t *testing.T) {
	t.Parallel()

	coupled := pkgInfo{
		ImportPath: "example.test/ws/beta", Dir: t.TempDir(), RepoRel: "beta",
		Imports: []string{"example.test/ws/alpha"},
	}
	standalone := pkgInfo{
		ImportPath: "example.test/ws/alpha", Dir: t.TempDir(), RepoRel: "alpha",
		Imports: []string{"fmt"},
	}

	t.Run("a coupled package is skipped, not failed", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			gobcoOut: "beta.go:3:8: no required module provides package",
			gobcoErr: errors.New("exit status 1"),
		}
		got, err := runGobcoAllPackages(t.Context(), runner, io.Discard,
			t.TempDir(), []pkgInfo{coupled}, wsImports, 2)
		if err != nil {
			t.Fatalf("runGobcoAllPackages err = %v, want the failure demoted", err)
		}
		if got[0].SkippedFor != "example.test/ws/alpha" {
			t.Errorf("SkippedFor = %q, want the coupled module named", got[0].SkippedFor)
		}
	})

	t.Run("an uncoupled package's failure still fails", func(t *testing.T) {
		t.Parallel()
		// Same gobco error, but this package imports nothing across
		// modules — so it is a real breakage and must surface.
		runner := &fakeRunner{
			gobcoOut: "alpha.go:1:1: syntax error",
			gobcoErr: errors.New("exit status 1"),
		}
		_, err := runGobcoAllPackages(t.Context(), runner, io.Discard,
			t.TempDir(), []pkgInfo{standalone}, wsImports, 2)
		if err == nil {
			t.Fatal("runGobcoAllPackages returned nil, want the real failure surfaced")
		}
	})

	t.Run("a coupled package that succeeds is measured normally", func(t *testing.T) {
		t.Parallel()
		// Absolute replace directives make gobco work even for a
		// coupled package; the demotion must not pre-empt a run that
		// would have produced data.
		runner := &fakeRunner{statsByDir: map[string]string{
			filepath.ToSlash(coupled.Dir): `[{"Start":"b.go:1:1","Code":"c","TrueCount":1,"FalseCount":1}]`,
		}}
		got, err := runGobcoAllPackages(t.Context(), runner, io.Discard,
			t.TempDir(), []pkgInfo{coupled}, wsImports, 2)
		if err != nil {
			t.Fatalf("runGobcoAllPackages: %v", err)
		}
		if got[0].SkippedFor != "" {
			t.Errorf("SkippedFor = %q, want empty for a successful run", got[0].SkippedFor)
		}
		if len(got[0].Records) != 1 {
			t.Errorf("records = %+v, want the measured condition", got[0].Records)
		}
	})
}

// TestWriteSkipNotice pins the reporting. A skipped package
// contributes no conditions, so the set has to be stated
// explicitly — otherwise a layer whose packages were all skipped
// reports "no conditions in scope" and passes, reading as a clean
// result rather than an unmeasured one.
func TestWriteSkipNotice(t *testing.T) {
	t.Parallel()

	t.Run("nothing skipped writes nothing", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		writeSkipNotice(&out, style.Style{}, []pkgStats{
			{Pkg: pkgInfo{RepoRel: "alpha"}},
		})
		if out.String() != "" {
			t.Errorf("output = %q, want nothing", out.String())
		}
	})

	t.Run("names each skipped package and its dependency", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		writeSkipNotice(&out, style.Style{}, []pkgStats{
			{Pkg: pkgInfo{RepoRel: "alpha"}},
			{Pkg: pkgInfo{RepoRel: "beta"}, SkippedFor: "example.test/ws/alpha"},
			{Pkg: pkgInfo{RepoRel: "gamma"}, SkippedFor: "example.test/ws/alpha"},
		})
		got := out.String()
		for _, want := range []string{"beta", "gamma", "example.test/ws/alpha", "2 package(s)"} {
			if !strings.Contains(got, want) {
				t.Errorf("output = %q, want it to mention %q", got, want)
			}
		}
		if strings.Contains(got, "alpha\n") && !strings.Contains(got, "beta") {
			t.Error("output names a measured package as skipped")
		}
	})
}
