// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package branch implements `ergon check branch`: per-layer
// branch-coverage gating backed by the `gobco` instrumenter.
//
// gobco rewrites Go source in a temp directory, runs `go test`
// against the rewritten copy, and emits a JSON stats file listing
// every condition (or branch, with `-branch`) plus its TrueCount /
// FalseCount across the test run. A condition is fully covered
// only when both arms recorded at least one hit.
//
// ergon enumerates the packages claimed by each declared layer in
// `.ergon.yaml`'s `checks.coverage.packages` (the same layer
// schema the line-coverage gate consumes), runs gobco per package
// in parallel, deduplicates the JSON records by source location,
// and aggregates branch coverage per layer. Verdicts:
//
//   - aggregate ≥ [Layer.Branch] → PASS.
//   - aggregate < [Layer.Branch] AND [Layer.RequireBranch] → FAIL.
//   - aggregate < [Layer.Branch] AND NOT [Layer.RequireBranch] →
//     informational (rendered without a failing verdict).
package branch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/checks/policy"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/style"
)

// RunOptions carries the per-invocation choices the cobra layer
// passes through to [Run]. Targets restricts which layers run;
// Workers caps the per-package gobco fan-out.
type RunOptions struct {
	// Targets is the optional list of layer prefixes the user
	// wants to exercise (`backend/golang`, `cli/...`). Empty means
	// "every declared layer".
	Targets []string

	// Workers caps the per-package gobco parallelism. Defaults to
	// runtime.NumCPU() / 2 when zero — gobco rebuilds each package
	// under test, so unrestrained fan-out saturates the build
	// cache and slows everything down.
	Workers int
}

// Run is the body of `ergon check branch`. Enumerates every Go
// package in the workspace via `go list ./...` per module, maps
// each to its longest-prefix declared layer, runs gobco on every
// in-scope package in parallel, then aggregates the per-condition
// hit counts into a per-layer branch-coverage percentage.
//
// Returns nil when every required layer (RequireBranch true) is
// at or above its declared Branch threshold. Informational
// layers (RequireBranch false) are rendered without affecting the
// return value.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, imports []modules.Import, cfg coverage.Config,
	excludes []policy.Exclude, skips []policy.Skip, opts RunOptions,
) error {
	s := style.Detect(stdout)
	if len(cfg.Packages) == 0 {
		s.Header(stdout, "branch", "per-layer branch-coverage thresholds")
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "  %s\n", s.Dimmed("— skipped (no thresholds declared in .ergon.yaml)"))
		fmt.Fprintln(stdout)
		return nil
	}

	pkgs, err := listWorkspacePackages(ctx, runner, root, imports)
	if err != nil {
		return err
	}

	stats, err := runGobcoAllPackages(ctx, runner, stderr, root, pkgs, imports, opts.Workers)
	if err != nil {
		return err
	}
	writeSkipNotice(stdout, s, stats)

	targets, claimIdx := coverage.SelectTargets(cfg.Packages, opts.Targets)
	if len(targets) == 0 {
		return fmt.Errorf("branch: no matching targets for %v", opts.Targets)
	}

	layerAgg := aggregateByLayer(stats, cfg.Packages, pkgs, excludes, skips)

	anyFailed := false
	for i, target := range targets {
		failed := renderTarget(stdout, s, target, layerAgg[claimIdx[i]])
		if failed {
			anyFailed = true
		}
	}

	fmt.Fprintln(stdout)
	if anyFailed {
		s.FinalVerdict(stderr, false,
			"one or more required layers below branch threshold")
		return errors.New("branch: one or more required layers below branch threshold")
	}
	s.FinalVerdict(stdout, true, "every required layer meets its branch threshold")
	return nil
}

// maxSkipDetailLines caps the per-package explanation. gobco's
// useful output is its first line or two; anything beyond that has
// been a stack frame in every case observed.
const maxSkipDetailLines = 2

// skipDetail renders the explanation lines for one skipped package.
//
// gobco fails by panicking, so reason carries a full goroutine dump
// below the message that names the cause. The dump describes the
// instrumenter's own call stack and says nothing about the package
// under test, so it is cut at the `goroutine ` marker and the
// remainder capped — twenty frames per skipped package buries the
// one line a reader needs.
//
// Falls back to naming the workspace dependency when gobco produced
// no output at all, which is the only thing known about the failure
// in that case.
func skipDetail(reason, dep string) []string {
	if strings.TrimSpace(reason) == "" {
		return []string{"imports " + dep}
	}
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(reason), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "goroutine ") {
			break
		}
		out = append(out, line)
		if len(out) == maxSkipDetailLines {
			break
		}
	}
	if len(out) == 0 {
		return []string{"imports " + dep}
	}
	return out
}

// writeSkipNotice reports every package gobco could not measure
// because it reaches across workspace modules, naming the
// dependency responsible.
//
// The notice is not cosmetic. A skipped package contributes no
// conditions, so the layers it belongs to report a percentage over
// a smaller denominator than the reader expects — and a layer whose
// packages are ALL skipped reports "no conditions in scope" and
// passes. Stating which packages were dropped is what keeps that
// from reading as a clean result.
func writeSkipNotice(w io.Writer, s style.Style, stats []pkgStats) {
	var skipped []pkgStats
	for _, st := range stats {
		if st.SkippedFor != "" {
			skipped = append(skipped, st)
		}
	}
	if len(skipped) == 0 {
		return
	}

	s.Header(w, "branch", "packages gobco could not measure")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", s.Dimmed(
		"gobco exited without producing coverage for these packages. Its own "+
			"output follows each one — the cause is usually in the instrumenter "+
			"rather than in the package."))
	fmt.Fprintln(w)
	for _, st := range skipped {
		fmt.Fprintf(w, "  %s   %s\n", s.Dimmed("SKIP"), st.Pkg.RepoRel)
		// gobco's own message when it produced one; the coupling
		// that qualified the package for demotion otherwise. Naming
		// the dependency alone reads as an accusation against it,
		// and in every case observed it was not at fault.
		for _, line := range skipDetail(st.SkipReason, st.SkippedFor) {
			fmt.Fprintf(w, "         %s\n", s.Dimmed(line))
		}
	}
	fmt.Fprintf(w, "\n  %s\n\n", s.Dimmed(fmt.Sprintf(
		"%d package(s) excluded from every layer aggregate below.", len(skipped))))
}

// renderTarget writes one per-layer section: header, branch
// coverage aggregate, and a verdict. Layers with require_branch:
// false render as informational (no FAIL even when the aggregate
// is below the threshold). Returns true when the layer must fail
// the run.
func renderTarget(stdout io.Writer, s style.Style, layer coverage.Layer, agg layerStats) bool {
	header := strings.TrimSuffix(layer.Path, "/...")
	details := fmt.Sprintf("branch ≥ %d%%", layer.Branch)
	if !layer.RequireBranch {
		details += "  (informational)"
	}
	s.Header(stdout, header, details)

	pct := agg.Pct()
	pass := pct >= float64(layer.Branch)
	// A partially-measured layer's percentage is computed over a
	// smaller denominator than the reader assumes, so the shortfall
	// is stated on the same line as the number it qualifies.
	partial := ""
	if agg.SkippedPkgs > 0 && agg.Total > 0 {
		partial = s.Dimmed(fmt.Sprintf(
			"   [%d package(s) not measured]", agg.SkippedPkgs))
	}
	fmt.Fprintf(stdout,
		"  Branch:     %5.1f%%  (%d / %d conditions fully covered)%s\n",
		pct, agg.Covered, agg.Total, partial)

	// The two zero-condition states are named here rather than
	// tested inline in the switch below. Go's coverage records a
	// block starting AFTER a case's expression list, so a condition
	// written in a case clause sits outside every covered block and
	// reads as unreachable to mutation testing however well it is
	// tested. Hoisting also lets each state carry its own name.
	empty := agg.Total == 0
	unmeasured := empty && agg.SkippedPkgs > 0

	switch {
	case unmeasured:
		// Measured nothing because its packages could not be
		// measured — NOT the same as having no conditions. Reporting
		// both as one verdict is how a gate that has stopped working
		// comes to look identical to a gate that is working, so this
		// state is named and, when the layer gates, fails.
		fmt.Fprintf(stdout, "  Verdict:    %s   %s\n\n",
			s.Fail(), s.Dimmed(fmt.Sprintf(
				"NOT MEASURED — %d package(s) skipped, 0 conditions collected",
				agg.SkippedPkgs)))
		if layer.RequireBranch {
			return true
		}
		return false
	case empty:
		fmt.Fprintf(stdout, "  Verdict:    %s\n\n", s.Dimmed("— no conditions in scope"))
		return false
	case pass:
		fmt.Fprintf(stdout, "  Verdict:    %s\n\n", s.Verdict(true))
		return false
	case layer.RequireBranch:
		fmt.Fprintf(stdout, "  Verdict:    %s\n\n", s.Verdict(false))
		return true
	default:
		fmt.Fprintf(stdout, "  Verdict:    %s\n\n",
			s.Dimmed("— below threshold (informational; set require_branch: true to gate)"))
		return false
	}
}

// listWorkspacePackages runs `go list -f '{{.ImportPath}} {{.Dir}}'
// ./...` once per workspace module and returns the union as
// [pkgInfo] records. Each record carries the import path (for
// layer-claim matching), the absolute filesystem dir (for gobco's
// per-package invocation), and the module-relative directory we
// use to translate the JSON's `file.go:line:col` start tokens
// back to repo-relative paths.
func listWorkspacePackages(
	ctx context.Context, runner xexec.Runner, root string, imports []modules.Import,
) ([]pkgInfo, error) {
	var out []pkgInfo
	for _, ip := range imports {
		moduleDir := filepath.Join(root, ip.Dir)
		var buf bytes.Buffer
		// Test imports are included: gobco runs the package's tests,
		// so a cross-module import reached only from a _test.go file
		// breaks the relocated build just the same.
		err := runner.Run(ctx,
			xexec.Options{Dir: moduleDir, Stdout: &buf, Stderr: &buf},
			"go", "list", "-f",
			"{{.ImportPath}} {{.Dir}}{{range .Imports}} {{.}}"+
				"{{end}}{{range .TestImports}} {{.}}{{end}}"+
				"{{range .XTestImports}} {{.}}{{end}}",
			"./...")
		if err != nil {
			return nil, fmt.Errorf(
				"branch: go list ./... in %s: %w: %s",
				ip.Dir, err, strings.TrimSpace(buf.String()),
			)
		}
		for line := range strings.SplitSeq(buf.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			importPath, pkgDir := fields[0], fields[1]
			rel, _ := filepath.Rel(root, pkgDir)
			out = append(out, pkgInfo{
				ImportPath: importPath,
				Dir:        pkgDir,
				RepoRel:    filepath.ToSlash(rel),
				Imports:    fields[2:],
			})
		}
	}
	return out, nil
}

// pkgInfo is one workspace Go package the branch gate considers.
type pkgInfo struct {
	// ImportPath is `go list`'s {{.ImportPath}} — used for the
	// longest-prefix layer claim against
	// [coverage.Config.Packages].
	ImportPath string

	// Dir is the absolute filesystem path gobco runs against.
	Dir string

	// RepoRel is the package directory relative to the repo root
	// in forward-slash form; combined with the JSON record's
	// `file:line:col` Start token to derive a repo-relative
	// file path the gate's excludes / skips match against.
	RepoRel string

	// Imports is every package this one imports, including from
	// its test files. Used by [coupledModule] to detect the
	// workspace layout gobco cannot measure.
	Imports []string
}

// coupledModule reports the import path of another workspace module
// this package depends on, if any.
//
// gobco instruments by copying the enclosing module to a temp
// directory and running `go test` there. A `go.work` file is a
// property of where a directory sits in the tree, so relocation
// always loses it — and a relative `replace` target (`../alpha`)
// stops resolving for the same reason. A package that reaches
// across workspace modules therefore cannot be measured, and
// gobco reports it as an opaque `[setup failed]`.
//
// Single-module repositories never match: with one entry in
// imports there is no other module to couple to.
func coupledModule(pkg pkgInfo, imports []modules.Import) (string, bool) {
	all := coupledModules(pkg, imports)
	if len(all) == 0 {
		return "", false
	}
	return all[0], true
}

// coupledModules returns every distinct workspace module, other
// than the package's own, that pkg imports.
//
// [coupledModule] answers "is this package coupled, and to what" —
// one name is enough to explain a demoted skip. Staging needs the
// complete set: every sibling whose relative `replace` has to be
// rewritten absolute, and a single name silently leaves the rest
// pointing at directories that do not exist beside the temp copy.
//
// go.thesmos.sh/testkit is the case that exposed the difference.
// Package cmd/testkit/cmds imports go.thesmos.sh/testkit/core/brand
// and go.thesmos.sh/testkit/generator; the first resolves to the
// root module and the second to a sibling, and go list emitted them
// in that order. Staging saw only the root, so cmd/go.mod kept
// `replace go.thesmos.sh/testkit/generator => ../generator`
// relative and gobco's copy failed to build.
func coupledModules(pkg pkgInfo, imports []modules.Import) []string {
	own := owningModuleDir(pkg.RepoRel, imports)
	seen := map[string]struct{}{}
	var out []string
	for _, imp := range pkg.Imports {
		// Attribute the import to the module with the LONGEST
		// matching path. Module paths nest — `example.test/ws` is a
		// prefix of `example.test/ws/alpha` — so any-match would
		// attribute `example.test/ws/alpha/sub` to the root module
		// and report a coupling for what is really an intra-module
		// import.
		owner, ownerDir := "", ""
		for _, ip := range imports {
			if ip.ImportPath == "" {
				continue
			}
			if imp != ip.ImportPath && !strings.HasPrefix(imp, ip.ImportPath+"/") {
				continue
			}
			if len(ip.ImportPath) > len(owner) {
				owner, ownerDir = ip.ImportPath, ip.Dir
			}
		}
		if owner == "" || ownerDir == own {
			continue
		}
		if _, dup := seen[owner]; dup {
			continue
		}
		seen[owner] = struct{}{}
		out = append(out, owner)
	}
	return out
}

// owningModuleDir returns the module directory that owns the
// package at repoRel — the longest module Dir that prefixes it.
func owningModuleDir(repoRel string, imports []modules.Import) string {
	best := ""
	bestLen := -1
	for _, ip := range imports {
		base := ip.Dir
		if base == "." {
			base = ""
		}
		matched := base == "" || repoRel == base || strings.HasPrefix(repoRel, base+"/")
		if matched && len(base) > bestLen {
			best, bestLen = ip.Dir, len(base)
		}
	}
	return best
}

// condRecord mirrors one entry in gobco's `-stats` JSON output:
// the source location, the original Go expression, and the per-
// arm hit counts for the test run.
type condRecord struct {
	Start      string `json:"Start"`
	Code       string `json:"Code"`
	TrueCount  int    `json:"TrueCount"`
	FalseCount int    `json:"FalseCount"`
}

// pkgStats pairs the package info with the JSON records gobco
// emitted for it. Used internally to thread per-package results
// through to the aggregator.
type pkgStats struct {
	Pkg     pkgInfo
	Records []condRecord

	// SkippedFor names the workspace module this package depends
	// on when gobco could not measure it (see [coupledModule]).
	// Empty when the package was measured. A skipped package
	// contributes no conditions, so [Run] reports the set
	// explicitly rather than letting the layer percentage quietly
	// be computed over a smaller denominator.
	SkippedFor string

	// SkipReason is gobco's own output for a skipped package — the
	// panic or error it exited with. Reported verbatim rather than
	// summarised: the causes observed in practice (a build-tag pair
	// type-checked together, a generic type alias the instrumenter
	// cannot parse) are not ones ergon can enumerate, and a notice
	// that names a cause it did not observe misdirects the reader.
	// Empty when the package was measured, or when the failure
	// produced no output.
	SkipReason string
}

// runGobcoAllPackages spawns a bounded worker pool that runs
// `gobco -branch -stats <tmp.json> <pkgdir>` once per workspace
// package and collects the parsed JSON records. The temp files
// are cleaned up before return.
func runGobcoAllPackages(
	ctx context.Context, runner xexec.Runner, stderr io.Writer,
	root string, pkgs []pkgInfo, imports []modules.Import, workers int,
) ([]pkgStats, error) {
	if workers <= 0 {
		workers = max(2, runtime.NumCPU()/2)
	}
	tmpDir, err := os.MkdirTemp("", "ergon-branch-*")
	if err != nil {
		return nil, fmt.Errorf("branch: tempdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Stage one alternate go.mod per module that reaches across
	// workspace modules, so gobco's relocated copy can still resolve
	// its siblings. Modules that need no staging map to a zero value
	// and are invoked without -modfile, leaving the single-module
	// path byte-identical to before.
	staged := map[string]stagedModfile{}
	for _, ip := range imports {
		sm, stageErr := stageModfile(
			root, ip.Dir, siblingsFor(ip.Dir, pkgs, imports), imports, tmpDir)
		if stageErr != nil {
			return nil, stageErr
		}
		staged[ip.Dir] = sm
	}

	type job struct {
		idx int
		pkg pkgInfo
	}
	type result struct {
		idx   int
		stats pkgStats
		err   error
	}

	jobs := make(chan job)
	results := make(chan result)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for j := range jobs {
				mod := staged[owningModuleDir(j.pkg.RepoRel, imports)]
				rec, gerr := runGobcoOne(ctx, runner, root, j.pkg,
					filepath.Join(tmpDir, fmt.Sprintf("%d.json", j.idx)), mod.Path)
				st := pkgStats{Pkg: j.pkg, Records: rec}
				// A failure is demoted to a skip only when the
				// package reaches across workspace modules — the
				// one shape gobco structurally cannot measure.
				// Every other failure still fails the run, so this
				// cannot mask a real breakage.
				if gerr != nil {
					if dep, coupled := coupledModule(j.pkg, imports); coupled {
						st.SkippedFor = dep
						// Carry gobco's own message forward: it is
						// the only account of why this package could
						// not be measured, and the notice has no
						// other basis on which to explain itself.
						st.SkipReason = gerr.Error()
						gerr = nil
					}
				}
				results <- result{idx: j.idx, stats: st, err: gerr}
			}
		})
	}
	go func() {
		for i, p := range pkgs {
			jobs <- job{idx: i, pkg: p}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	out := make([]pkgStats, len(pkgs))
	var firstErr error
	for r := range results {
		out[r.idx] = r.stats
		if r.err != nil && firstErr == nil {
			firstErr = r.err
			fmt.Fprintf(stderr, "branch: %s: %v\n", r.stats.Pkg.ImportPath, r.err)
		}
	}
	if firstErr != nil {
		return out, firstErr
	}
	return out, nil
}

// runGobcoOne runs `gobco -branch -stats <statsPath> <pkg.Dir>`
// and decodes the resulting JSON. A package with no instrumentable
// branches returns an empty record list with nil error — gobco
// prints "nothing to instrument" and skips writing the stats file.
//
// modfilePath, when non-empty, is forwarded to the underlying
// `go test` as `-modfile` via gobco's `-test` passthrough. It names
// a staged go.mod whose intra-workspace replaces are absolute, which
// is what lets gobco's relocated copy resolve sibling modules — see
// [stageModfile].
//
// # Environment
//
// gobco runs under GODEBUG=gotypesalias=1. Its own go.mod declares
// `go 1.16`, and the toolchain derives GODEBUG defaults from that
// directive — 1.16 predates generic type aliases, so gotypesalias
// defaults to 0 and the instrumenter's type-checker rejects any
// package whose dependency graph contains one, panicking before it
// instruments anything. Setting it here rather than documenting a
// workaround keeps the gate measuring what it claims to measure:
// on go.thesmos.sh/testkit the override recovered 177 of the
// engine layer's 601 conditions.
func runGobcoOne(
	ctx context.Context, runner xexec.Runner,
	_ string, pkg pkgInfo, statsPath, modfilePath string,
) ([]condRecord, error) {
	var buf bytes.Buffer
	args := []string{"-branch", "-stats", statsPath}
	if modfilePath != "" {
		args = append(args, "-test", "-modfile="+modfilePath)
	}
	args = append(args, ".")
	err := runner.Run(ctx,
		xexec.Options{
			Dir:    pkg.Dir,
			Stdout: &buf,
			Stderr: &buf,
			Env:    []string{"GODEBUG=gotypesalias=1"},
		},
		"gobco", args...)
	if err != nil {
		// "nothing to instrument" is gobco's non-zero exit for a
		// package without conditionals. Treat as empty data; the
		// aggregator simply contributes zero conditions for it.
		if strings.Contains(buf.String(), "nothing to instrument") {
			return nil, nil
		}
		return nil, fmt.Errorf("gobco: %w: %s", err, strings.TrimSpace(buf.String()))
	}
	body, err := os.ReadFile(statsPath)
	if err != nil {
		// Stats file may be absent when gobco found nothing to do.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("branch: read %s: %w", statsPath, err)
	}
	var recs []condRecord
	if err := json.Unmarshal(body, &recs); err != nil {
		return nil, fmt.Errorf("branch: parse %s: %w", statsPath, err)
	}
	return recs, nil
}

// layerStats is the per-layer aggregate the renderer prints and
// the verdict compares against [coverage.Layer.Branch].
type layerStats struct {
	Total   int // number of conditions claimed by the layer
	Covered int // subset of Total whose TrueCount > 0 AND FalseCount > 0

	// SkippedPkgs counts the packages claimed by this layer that
	// gobco could not measure. It is what separates "this layer has
	// no conditions" from "this layer was not measured" — two states
	// that otherwise both report Total == 0 and would render the
	// same verdict.
	SkippedPkgs int
}

// Pct returns the layer's branch-coverage percentage in the half-
// open interval [0, 100]. A layer with zero conditions in scope
// reports 0 so the verdict renderer can detect the empty case.
func (s layerStats) Pct() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Covered) * 100 / float64(s.Total)
}

// aggregateByLayer maps every gobco condition to its longest-
// prefix declared layer (via the package's ImportPath) and
// accumulates total + fully-covered counts per layer. Excludes
// and skips are honoured against the condition's repo-relative
// file path — the same policy the line-coverage gate applies.
func aggregateByLayer(
	stats []pkgStats, packages []coverage.Layer, _ []pkgInfo,
	excludes []policy.Exclude, skips []policy.Skip,
) []layerStats {
	out := make([]layerStats, len(packages))
	// Deduplicate records by their fully-qualified location
	// (RepoRel + Start) so a condition that surfaces in multiple
	// gobco runs (cross-package overlap is unusual but possible
	// with build-tagged files) does not double-count.
	type key struct {
		file  string
		start string
	}
	type agg struct {
		layerIdx int
		t, f     int
	}
	seen := map[key]*agg{}
	for _, ps := range stats {
		// An unmeasured package is attributed to its layer so the
		// renderer can tell an empty layer from an unmeasured one.
		if ps.SkippedFor != "" {
			if idx := coverage.LongestPrefixLayerIdx(packages, ps.Pkg.RepoRel); idx >= 0 {
				out[idx].SkippedPkgs++
			}
			continue
		}
		for _, r := range ps.Records {
			file := relativeFile(ps.Pkg.RepoRel, r.Start)
			if policy.MatchesExclude(file, excludes) {
				continue
			}
			if policy.MatchesSkip("", file, skips) {
				continue
			}
			idx := coverage.LongestPrefixLayerIdx(packages, ps.Pkg.RepoRel)
			if idx < 0 {
				continue
			}
			k := key{file: file, start: r.Start}
			a, ok := seen[k]
			if !ok {
				a = &agg{layerIdx: idx}
				seen[k] = a
			}
			a.t += r.TrueCount
			a.f += r.FalseCount
		}
	}
	for _, a := range seen {
		out[a.layerIdx].Total++
		if a.t > 0 && a.f > 0 {
			out[a.layerIdx].Covered++
		}
	}
	return out
}

// relativeFile joins the package's repo-relative directory with
// the leading `file.go:` portion of a gobco Start token to
// produce a repo-relative file path the policy matchers consume.
func relativeFile(pkgRepoRel, start string) string {
	// Start looks like `file.go:4:8`; we keep only the file part.
	idx := strings.Index(start, ":")
	file := start
	if idx > 0 {
		file = start[:idx]
	}
	if pkgRepoRel == "" || pkgRepoRel == "." {
		return file
	}
	return pkgRepoRel + "/" + file
}
