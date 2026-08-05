// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.thesmos.sh/ergon/internal/checks/policy"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/style"
)

// RunOptions carries the per-invocation overrides the cobra layer
// passes through to [Run]. Targets restricts which layers run;
// Verbose dumps the count=0 block ranges of failing files.
type RunOptions struct {
	// Targets is the list of layer prefixes the user wants to
	// exercise (e.g. `foundation`, `core/kernel/fold`). Empty
	// means "every layer in cfg.Packages".
	Targets []string

	// Verbose, when true, appends an "Uncovered ranges" section
	// after every failing target showing the file:start-end
	// blocks the test suite did not exercise.
	Verbose bool
}

// Run is `ergon check coverage`: merges every per-module `.out`
// profile under coverageDir, invokes `go tool cover -func`, then
// emits a per-target report and fails any function whose coverage
// falls below its layer's threshold.
//
// imports pairs every workspace module with the import path it
// declares in go.mod. Run uses the mapping to translate `go tool
// cover` output (which prints full import paths) into repo-
// relative paths so layer globs in `.ergon.yaml` can be written
// uniformly across multi-module repositories where submodule
// import paths diverge from the root.
//
// excludes and skips are the shared [policy.Exclude] and
// [policy.Skip] lists; mutation reads the same lists. A function
// whose path matches an exclude is counted under "excluded";
// one whose (func name, file path) satisfies a skip is counted
// under "skipped". Neither contributes to the failure count.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root, coverageDir string, imports []modules.Import, cfg Config,
	excludes []policy.Exclude, skips []policy.Skip, opts RunOptions,
) error {
	cfg = withDefaults(cfg)
	if len(cfg.Packages) == 0 {
		s := style.Detect(stdout)
		s.Header(stdout, "coverage", "per-layer line thresholds")
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "  %s\n", s.Dimmed("— skipped (no thresholds declared in .ergon.yaml)"))
		fmt.Fprintln(stdout)
		return nil
	}

	profiles, err := findProfiles(coverageDir)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return fmt.Errorf("coverage: no coverprofiles found in %s — run `ergon test` first", coverageDir)
	}

	mergedPath, mergedBody, cleanup, err := mergeProfiles(profiles)
	if err != nil {
		return err
	}
	defer cleanup()

	funcLog, err := captureFuncCoverage(ctx, runner, root, mergedPath)
	if err != nil {
		return fmt.Errorf("coverage: go tool cover -func: %w", err)
	}

	rows := parseFuncLog(funcLog)
	prefixes := sortedPrefixes(imports)

	// Each row is claimed by its longest-prefix declared layer
	// in cfg.Packages. The per-layer renderer below shows only
	// the rows its declared layer claimed, so nested layers
	// override their parents (declaring `backend/golang/...` at
	// 80% takes those rows out of a parent `backend/...` at 90%).
	rowClaim := claimRows(cfg.Packages, prefixes, rows)

	// Per-layer aggregate statement coverage. Drives the PASS /
	// FAIL verdict; the per-function rows above remain as the
	// failure-diagnostic table only. Matches `go test -cover`'s
	// "X% of statements" semantics so what the runner shows and
	// what the gate enforces agree.
	funcSpans := indexFunctionsByFile(funcLog, prefixes)
	layerAgg := aggregateByLayer(mergedBody, cfg.Packages, prefixes,
		excludes, skips, funcSpans)

	targets, claimIdx := SelectTargets(cfg.Packages, opts.Targets)
	if len(targets) == 0 {
		return fmt.Errorf("coverage: no matching targets for %v", opts.Targets)
	}

	s := style.Detect(stdout)
	anyFailed := false

	for i, target := range targets {
		failed := renderTarget(stdout, s, target, claimIdx[i],
			rows, rowClaim, layerAgg[claimIdx[i]],
			excludes, skips, prefixes,
			cfg.TopN, opts.Verbose, mergedBody)
		if failed {
			anyFailed = true
		}
	}

	if anyFailed {
		s.FinalVerdict(stderr, false,
			"one or more functions below threshold (see per-target reports above)")
		return fmt.Errorf("coverage: one or more functions below threshold")
	}
	s.FinalVerdict(stdout, true, "every gated function meets its layer threshold")
	return nil
}

// layerMatchPrefix normalises a layer-glob path into the prefix
// the per-function classifier compares against repo-relative
// paths. The empty string is the workspace-wide sentinel — every
// row matches. `./...`, `./`, `.`, `...`, and the empty layer
// path all flatten to that sentinel.
func layerMatchPrefix(layerPath string) string {
	p := strings.TrimSuffix(layerPath, "/...")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimSuffix(p, "/")
	if p == "." || p == "..." {
		return ""
	}
	return p
}

// mergedBlock is one source block in the merged coverprofile
// after duplicates (cross-module `-coverpkg` writes the same
// block into every per-module profile) are collapsed by location
// and their execution counts summed.
type mergedBlock struct {
	// Path is the raw import-path-form file token from the
	// profile (`go.example.com/proj/cli/foo.go`); callers convert
	// to repo-relative form via [toRepoRelative].
	Path string

	// Span is the literal `<sline>.<scol>,<eline>.<ecol>`
	// substring — kept verbatim so re-emitting matches the
	// original profile shape.
	Span string

	// StartLine / EndLine are the integer line numbers extracted
	// from Span for the renderers that present ranges to the
	// user.
	StartLine int
	EndLine   int

	// Stmts is `numStmts` from the profile row — the same value
	// every duplicate emits, so it survives untouched through the
	// merge.
	Stmts int

	// Count is the SUM of execution counts across every duplicate
	// of this block in the merged profile. A block whose Count
	// remains zero after the merge is genuinely uncovered.
	Count int
}

// parseMergedBlocks walks the concatenated coverprofile body,
// deduplicates blocks by (path + span), and returns one
// [mergedBlock] per distinct location. The atomic-mode SUM of
// per-profile counts matches what `go tool cover -func` derives
// internally, so the gate's aggregate agrees with the runner.
func parseMergedBlocks(merged string) []mergedBlock {
	type key struct {
		path string
		span string
	}
	dedup := map[key]*mergedBlock{}
	for line := range strings.SplitSeq(merged, "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		path, span, ok := splitProfileHead(fields[0])
		if !ok {
			continue
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		k := key{path: path, span: span}
		b, found := dedup[k]
		if !found {
			startEnd := strings.SplitN(span, ",", 2)
			if len(startEnd) != 2 {
				continue
			}
			startLine, err := strconv.Atoi(strings.SplitN(startEnd[0], ".", 2)[0])
			if err != nil {
				continue
			}
			endLine, err := strconv.Atoi(strings.SplitN(startEnd[1], ".", 2)[0])
			if err != nil {
				continue
			}
			b = &mergedBlock{
				Path: path, Span: span,
				StartLine: startLine, EndLine: endLine,
				Stmts: stmts,
			}
			dedup[k] = b
		}
		b.Count += count
	}
	out := make([]mergedBlock, 0, len(dedup))
	for _, b := range dedup {
		out = append(out, *b)
	}
	return out
}

// layerStats records the per-layer aggregate statement coverage
// — the headline number the gate verdict compares to
// [Layer.Line]. Matches `go test -cover`'s reported percentage so
// the runner output and the gate agree.
type layerStats struct {
	// TotalStmts is the number of statements claimed by the
	// layer after excludes and skips drop ineligible blocks.
	TotalStmts int

	// CoveredStmts is the subset of TotalStmts whose execution
	// count was > 0 in the merged coverprofile.
	CoveredStmts int
}

// Pct returns the layer's aggregate coverage percentage in the
// half-open interval [0, 100]. Returns 0 when the layer has zero
// claimed statements so an empty layer never fails the gate by
// accident.
func (s layerStats) Pct() float64 {
	if s.TotalStmts == 0 {
		return 0
	}
	return float64(s.CoveredStmts) * 100 / float64(s.TotalStmts)
}

// aggregateByLayer attributes every block in the merged
// coverprofile to its longest-prefix declared layer in packages,
// applies excludes (file-path glob) and skips (func name + file
// glob), and returns one [layerStats] entry per declared layer.
//
// Blocks are deduplicated before accumulation: with cross-module
// `-coverpkg`, the same source block appears in multiple
// per-module profiles. The key (path + span) holds the SUM of
// execution counts across every appearance — matching the
// dedup-and-sum behaviour `go tool cover -func` applies to a
// merged profile.
//
// Blocks whose containing layer is unknown (no declared layer
// matches and no wildcard exists) are dropped silently — the
// gate cannot enforce a threshold on rows it has no layer for.
func aggregateByLayer(
	merged string, packages []Layer, prefixes []modules.Import,
	excludes []policy.Exclude, skips []policy.Skip,
	funcSpans map[string][]funcSpan,
) []layerStats {
	out := make([]layerStats, len(packages))
	for _, b := range parseMergedBlocks(merged) {
		rel := toRepoRelative(prefixes, b.Path)
		idx := LongestPrefixLayerIdx(packages, rel)
		if idx < 0 {
			continue
		}
		if policy.MatchesExclude(rel, excludes) {
			continue
		}
		funcName := functionAt(funcSpans[rel], b.StartLine)
		if policy.MatchesSkip(funcName, rel, skips) {
			continue
		}
		out[idx].TotalStmts += b.Stmts
		if b.Count > 0 {
			out[idx].CoveredStmts += b.Stmts
		}
	}
	return out
}

// claimRows assigns every row to a declared-layer index in
// cfg.Packages — the layer with the longest prefix that covers
// the row's repo-relative path. A row that no layer covers (and
// no wildcard `./...` exists) reports -1; the renderer drops
// those from every report.
//
// Specificity wins: a row under `backend/golang/...` claims that
// layer, NOT the parent `backend/...` even when both are
// declared. This ensures a nested layer's threshold overrides
// the parent for the rows it claims.
func claimRows(packages []Layer, prefixes []modules.Import, rows []funcRow) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		out[i] = LongestPrefixLayerIdx(packages, toRepoRelative(prefixes, r.Path))
	}
	return out
}

// LongestPrefixLayerIdx returns the index of the declared layer
// with the longest base that covers rel, or -1 when no layer
// matches. The wildcard sentinel (`./...`) covers every row at
// length zero so concrete layers always win when both apply.
func LongestPrefixLayerIdx(packages []Layer, rel string) int {
	bestIdx := -1
	bestLen := -1
	for i, p := range packages {
		base := layerMatchPrefix(p.Path)
		matches := base == "" || base == rel || strings.HasPrefix(rel, base+"/")
		if !matches {
			continue
		}
		if len(base) > bestLen {
			bestIdx = i
			bestLen = len(base)
		}
	}
	return bestIdx
}

// sortedPrefixes returns imports sorted by descending ImportPath
// length so [toRepoRelative] picks the most-specific module when
// two share a prefix (e.g. `foo/proj` vs `foo/proj/cli`).
func sortedPrefixes(imports []modules.Import) []modules.Import {
	out := slices.Clone(imports)
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].ImportPath) > len(out[j].ImportPath)
	})
	return out
}

// toRepoRelative converts a `go tool cover` path token (a full
// import path like `go.example.com/proj/cli/internal/foo.go`)
// into the repo-relative form `.ergon.yaml`'s layer globs match
// against (e.g. `cli/internal/foo.go`).
//
// Matching iterates the sorted prefix list (longest-first) so a
// submodule whose import path nests under another's wins over
// the parent. A token with no matching import surfaces verbatim
// — the caller sees an unfamiliar path rather than a silently
// mis-classified row.
func toRepoRelative(prefixes []modules.Import, p string) string {
	for _, m := range prefixes {
		if p == m.ImportPath {
			if m.Dir == "." {
				return ""
			}
			return m.Dir
		}
		if !strings.HasPrefix(p, m.ImportPath+"/") {
			continue
		}
		rest := strings.TrimPrefix(p, m.ImportPath+"/")
		if m.Dir == "." {
			return rest
		}
		return m.Dir + "/" + rest
	}
	return p
}

// withDefaults fills any zero-value field on cfg from [Defaults].
func withDefaults(cfg Config) Config {
	if cfg.TopN == 0 {
		cfg.TopN = Defaults().TopN
	}
	return cfg
}

// SelectTargets filters cfg.Packages by the user-supplied
// targets. With no targets every package is selected as declared.
//
// For each requested target, the longest-prefix matching layer
// supplies the threshold; the returned layer's Path narrows to the
// request. This mirrors the per-function classification rule:
// when two layers overlap (e.g. `internal/...` and
// `internal/checks/...`), only the more-specific one applies.
func SelectTargets(packages []Layer, requested []string) ([]Layer, []int) {
	if len(requested) == 0 {
		idxs := make([]int, len(packages))
		for i := range packages {
			idxs[i] = i
		}
		return packages, idxs
	}
	var (
		out  []Layer
		idxs []int
	)
	for _, t := range requested {
		t = strings.TrimSuffix(t, "/")
		bestIdx := -1
		bestLen := -1
		for i, p := range packages {
			base := layerMatchPrefix(p.Path)
			// A user-supplied target must match a CONCRETE
			// declared layer; the workspace-wide wildcard
			// sentinel does not count here, so a typo
			// (`ergon check coverage typoo`) errors out instead
			// of silently inheriting the wildcard threshold.
			if base == "" {
				continue
			}
			if base != t && !strings.HasPrefix(t, base+"/") {
				continue
			}
			if len(base) > bestLen {
				bestIdx = i
				bestLen = len(base)
			}
		}
		if bestIdx < 0 {
			continue
		}
		match := packages[bestIdx]
		base := strings.TrimSuffix(match.Path, "/...")
		path := t + "/..."
		if base == t {
			path = base + "/..."
		}
		out = append(out, Layer{
			Path:          path,
			Line:          match.Line,
			Branch:        match.Branch,
			RequireBranch: match.RequireBranch,
		})
		idxs = append(idxs, bestIdx)
	}
	return out, idxs
}

// longestPrefixLayer returns the declared layer whose base is the
// longest prefix of t. Reports ok=false when no layer covers t at
// all.
func longestPrefixLayer(packages []Layer, t string) (Layer, bool) {
	var (
		best    Layer
		bestLen = -1
	)
	for _, p := range packages {
		base := layerMatchPrefix(p.Path)
		// A wildcard-everything layer (`./...`, `...`) carries the
		// empty-string sentinel; treat it as a length-zero match
		// so concrete layers always win when they overlap.
		matches := base == "" || base == t || strings.HasPrefix(t, base+"/")
		if !matches {
			continue
		}
		if len(base) > bestLen {
			best = p
			bestLen = len(base)
		}
	}
	return best, bestLen >= 0
}

// renderTarget writes one per-layer section: header, the
// aggregate coverage line, the PASS/FAIL verdict (driven by the
// aggregate), and — only when the layer fails — a "lowest-
// coverage functions" diagnostic table so the reader can see
// where the deficit lives. Returns true when the layer's
// aggregate is below [Layer.Line].
//
// The aggregate matches `go test -cover ./<layer>/...`: per-block
// statement coverage summed across the layer, excludes and skips
// applied at block granularity. Individual functions never drive
// the verdict — a layer with one untested handler still passes
// if the rest of the layer covers enough statements to clear the
// bar.
func renderTarget(
	stdout io.Writer, s style.Style, layer Layer, claimIdx int,
	rows []funcRow, rowClaim []int, agg layerStats,
	excludes []policy.Exclude,
	skips []policy.Skip, prefixes []modules.Import, topN int, verbose bool,
	mergedBody string,
) bool {
	prefix := layerMatchPrefix(layer.Path)
	header := strings.TrimSuffix(layer.Path, "/...")
	s.Header(stdout, header, fmt.Sprintf("line ≥ %d%%", layer.Line))

	// Per-function pass/below counts retained as a diagnostic
	// detail; they no longer drive the verdict. Excluded and
	// skipped rows are tracked so the user can see the policy
	// took effect even though the aggregate is the threshold.
	var (
		passing, below, skipped, excluded int
		failures                          []Failure
	)
	for i, r := range rows {
		// Specificity-wins: a row belongs to the rendered target
		// iff its longest-prefix declared layer is the one this
		// target derives from. A more-specific layer claims its
		// rows out of any parent's report.
		if rowClaim[i] != claimIdx {
			continue
		}
		rel := toRepoRelative(prefixes, r.Path)
		if prefix != "" && !strings.HasPrefix(rel, prefix+"/") && rel != prefix {
			continue
		}
		if policy.MatchesExclude(rel, excludes) {
			excluded++
			continue
		}
		if policy.MatchesSkip(r.Func, rel, skips) {
			skipped++
			continue
		}
		if int(r.Pct) >= layer.Line {
			passing++
			continue
		}
		below++
		failures = append(failures, Failure{
			Path: rel, Func: r.Func, Pct: r.Pct, Threshold: layer.Line, Layer: prefix,
		})
	}

	pct := agg.Pct()
	pass := pct >= float64(layer.Line)
	fmt.Fprintf(stdout,
		"  Coverage:   %5.1f%%  (%d / %d statements covered)\n",
		pct, agg.CoveredStmts, agg.TotalStmts)
	fmt.Fprintf(stdout,
		"  Functions:  %d ≥ threshold · %d below · %d skipped · %d excluded\n",
		passing, below, skipped, excluded)

	if pass {
		fmt.Fprintf(stdout, "  Verdict:    %s\n\n", s.Verdict(true))
		return false
	}

	fmt.Fprintf(stdout, "  Verdict:    %s\n\n", s.Verdict(false))

	sort.SliceStable(failures, func(i, j int) bool {
		return failures[i].Pct < failures[j].Pct
	})

	if len(failures) > 0 {
		fmt.Fprintf(stdout, "  %s\n", s.Bolded("Lowest-coverage functions"))
		limit := min(topN, len(failures))
		for _, f := range failures[:limit] {
			fmt.Fprintf(stdout, "    %5.1f%%  %s  %s\n", f.Pct, f.Path, s.Dimmed(f.Func))
		}
		if len(failures) > limit {
			fmt.Fprintf(stdout, "    %s\n", s.Dimmed(fmt.Sprintf("… and %d more function(s)", len(failures)-limit)))
		}
	}

	if verbose {
		writeUncoveredRanges(stdout, s, failures, prefixes, mergedBody)
	}
	fmt.Fprintln(stdout)
	return true
}

// writeUncoveredRanges appends the uncovered block ranges (file
// + line span + statement count) for every failing file in
// failures. Ranges come from the merged coverprofile's `count=0`
// rows.
func writeUncoveredRanges(
	w io.Writer, s style.Style, failures []Failure, prefixes []modules.Import, merged string,
) {
	files := uniqueFiles(failures)
	if len(files) == 0 {
		return
	}
	fmt.Fprintf(w, "\n  %s   %s\n", s.Bolded("Uncovered ranges"), s.Dimmed("(file:start-end (stmts))"))
	for _, b := range parseMergedBlocks(merged) {
		if b.Count > 0 {
			continue
		}
		rel := toRepoRelative(prefixes, b.Path)
		if !slices.Contains(files, rel) {
			continue
		}
		fmt.Fprintf(w, "    %s:%d-%d (%d stmts)\n", rel, b.StartLine, b.EndLine, b.Stmts)
	}
}

// splitProfileHead pulls the path and start/end positions out of
// the leading <path>:<sline>.<scol>,<eline>.<ecol> token of a
// coverprofile row.
func splitProfileHead(s string) (path, span string, ok bool) {
	idx := strings.LastIndex(s, ":")
	if idx <= 0 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

// uniqueFiles returns the deduplicated set of file paths covered
// by failures, preserving first-occurrence order.
func uniqueFiles(failures []Failure) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, f := range failures {
		if _, dup := seen[f.Path]; dup {
			continue
		}
		seen[f.Path] = struct{}{}
		out = append(out, f.Path)
	}
	return out
}

// findProfiles returns every `*.out` file under dir, sorted by
// name.
func findProfiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("coverage: read %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".out") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

// writeMergedProfile writes body to w in one call and reports any
// shortfall.
//
// The merge previously wrote line by line with fmt.Fprintln and
// discarded both return values. A write that stopped partway — a
// filesystem out of space returns the bytes it managed before
// failing — silently lost the tail of one record, and the next
// line's bytes landed directly against it, producing one malformed
// record spanning two modules:
//
//	...ttl.go:72.2,72.12 go.thesmos.sh/testkit/core/trace
//
// `go tool cover` rejected that as a malformed import path, which
// was the fortunate outcome: a truncation that still parsed would
// have produced a quietly wrong percentage instead.
//
// n is checked alongside err because [io.Writer] permits a short
// write with a nil error. [os.File] converts that case to
// [io.ErrShortWrite] itself, but checking here keeps the guarantee
// independent of which writer is handed in.
func writeMergedProfile(w io.Writer, body string) error {
	n, err := io.WriteString(w, body)
	if err != nil {
		return fmt.Errorf("coverage: write merged profile: %w", err)
	}
	if n != len(body) {
		return fmt.Errorf("coverage: write merged profile: %w: %d of %d bytes",
			io.ErrShortWrite, n, len(body))
	}
	return nil
}

// mergeProfiles concatenates every input profile into a single
// temp file. Returns the path, the merged body (used for verbose
// mode's uncovered-range dump), and a cleanup function.
func mergeProfiles(paths []string) (string, string, func(), error) {
	f, err := os.CreateTemp("", "ergon-cov-*.out")
	if err != nil {
		return "", "", func() {}, fmt.Errorf("coverage: create temp: %w", err)
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	var body strings.Builder
	modeWritten := false
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			cleanup()
			return "", "", func() {}, fmt.Errorf("coverage: read %s: %w", p, err)
		}
		for line := range strings.SplitSeq(string(raw), "\n") {
			if strings.HasPrefix(line, "mode:") {
				if !modeWritten {
					body.WriteString(line + "\n")
					modeWritten = true
				}
				continue
			}
			if line == "" {
				continue
			}
			body.WriteString(line + "\n")
		}
	}

	// The file is written from body rather than accumulated
	// alongside it: the bytes `go tool cover` parses and the string
	// [aggregateByLayer] walks are then the same data by
	// construction. Maintained in parallel, a failed file write left
	// the two disagreeing, and the layer percentages were computed
	// from records the tool never saw.
	merged := body.String()
	if err := writeMergedProfile(f, merged); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	// Closed before the path is handed on. Some filesystems report a
	// deferred write failure only at close, and `go tool cover` has
	// no use for an open descriptor.
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", "", func() {}, fmt.Errorf("coverage: close merged profile: %w", err)
	}
	name := f.Name()
	return name, merged, func() { _ = os.Remove(name) }, nil
}

// captureFuncCoverage runs `go tool cover -func <profile>` and
// returns the captured stdout.
func captureFuncCoverage(
	ctx context.Context, runner xexec.Runner, root, profile string,
) (string, error) {
	var buf bytes.Buffer
	err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &buf, Stderr: &buf},
		"go", "tool", "cover", "-func="+profile)
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(buf.String()))
	}
	return buf.String(), nil
}

// funcRow records one function's coverage line as reported by
// `go tool cover -func`.
type funcRow struct {
	Path string
	Func string
	Pct  float64
}

// parseFuncLog converts `go tool cover -func` output into a slice
// of [funcRow]. The `total:` summary row is dropped.
func parseFuncLog(out string) []funcRow {
	var rows []funcRow
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "total:" {
			continue
		}
		pctStr := strings.TrimSuffix(fields[len(fields)-1], "%")
		pct, err := strconv.ParseFloat(pctStr, 64)
		if err != nil {
			continue
		}
		path := stripFileLocation(fields[0])
		funcName := strings.Join(fields[1:len(fields)-1], " ")
		rows = append(rows, funcRow{Path: path, Func: funcName, Pct: pct})
	}
	return rows
}

// stripFileLocation removes the trailing `:<line>:` from a `go
// tool cover -func` path token.
func stripFileLocation(s string) string {
	s = strings.TrimSuffix(s, ":")
	if idx := strings.LastIndex(s, ":"); idx > 0 {
		return s[:idx]
	}
	return s
}

// Failure records one threshold violation for reporting.
type Failure struct {
	Path      string
	Func      string
	Pct       float64
	Threshold int
	Layer     string
}
