// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"go.thesmos.sh/ergon/internal/checks/policy"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/style"
)

// UncoveredOptions controls the [Uncovered] subcommand's
// filtering behaviour. By default Uncovered respects the same
// policy the gate command applies (`checks.coverage.packages`
// layers + shared `checks.excludes` / `checks.skips`); the
// All flag flips it to "show every count=0 block regardless".
type UncoveredOptions struct {
	// All disables every policy filter so the report covers
	// every uncovered block across every package the test suite
	// touched — useful when chasing coverage debt that the gate
	// intentionally excluded.
	All bool
}

// UncoveredBlock records one `count=0` region from the merged
// coverprofile: the source span and the number of statements it
// contains. The path and the containing function are held
// outside the struct by the caller (results are grouped by
// file + function before rendering).
type UncoveredBlock struct {
	// StartLine is the first line of the block (inclusive).
	StartLine int

	// EndLine is the last line of the block (inclusive).
	EndLine int

	// Stmts is the number of statements in the block as
	// `go tool cover` reports it.
	Stmts int
}

// funcSpan records one function's starting line + name as
// reported by `go tool cover -func`. The function is assumed to
// extend to the next funcSpan's starting line (or to the end of
// the file for the last entry); the cover output does not name
// end lines explicitly.
type funcSpan struct {
	StartLine int
	Func      string
}

// Uncovered runs `go tool cover -func` against the merged
// coverprofile, maps every `count=0` block to its containing
// function, and renders the result grouped by file → function.
// By default the renderer applies the same policy the
// gate-bound [Run] applies: only functions under
// `checks.coverage.packages` layers, minus the shared
// excludes / skips. Pass UncoveredOptions{All: true} to bypass
// the filter and show every uncovered block.
//
// Run `ergon test` first to produce the per-module `.out`
// profiles; Uncovered merges them in memory and walks the
// result.
//
// imports pairs every workspace module with its declared import
// path; Uncovered maps `go tool cover` output back to repo-
// relative paths via the longest matching import prefix so reports
// stay consistent across multi-module repositories.
func Uncovered(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root, coverageDir string, imports []modules.Import,
	cfg Config, excludes []policy.Exclude, skips []policy.Skip,
	opts UncoveredOptions,
) error {
	_ = stderr
	s := style.Detect(stdout)
	details := "every count=0 block, grouped by function"
	if opts.All {
		details += " · --all (no filters)"
	} else {
		details += " · filtered by checks policy (pass --all to disable)"
	}
	s.Header(stdout, "coverage uncovered", details)

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
	prefixes := sortedPrefixes(imports)
	spans := indexFunctionsByFile(funcLog, prefixes)

	byFileBlocks := collectUncoveredBlocks(mergedBody, prefixes)
	grouped := groupByFunction(byFileBlocks, spans, cfg.Packages, excludes, skips, opts.All)
	renderUncovered(stdout, s, grouped)
	return nil
}

// fileGroup is one (file, []functions) entry in the rendered
// report. Files sort alphabetically; functions within each file
// sort by start line.
type fileGroup struct {
	Path  string
	Funcs []funcGroup
}

// funcGroup is one (function, []blocks) entry inside a file.
// Blocks within each function sort by start line.
type funcGroup struct {
	Func   string
	Blocks []UncoveredBlock
}

// groupByFunction takes the file → blocks map produced by
// [collectUncoveredBlocks] and the file → funcSpans map produced
// by [indexFunctionsByFile] and returns a per-file, per-function
// grouping suitable for the renderer. When all=false the policy
// filters (layers, excludes, skips) drop entries that the gate
// command would also drop.
func groupByFunction(
	byFile map[string][]UncoveredBlock,
	spans map[string][]funcSpan,
	layers []Layer,
	excludes []policy.Exclude, skips []policy.Skip,
	all bool,
) []fileGroup {
	paths := make([]string, 0, len(byFile))
	for p := range byFile {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out []fileGroup
	for _, path := range paths {
		if !all && filterFile(path, layers, excludes) {
			continue
		}
		byFunc := map[string][]UncoveredBlock{}
		for _, b := range byFile[path] {
			funcName := functionAt(spans[path], b.StartLine)
			if !all && filterFunc(funcName, path, skips) {
				continue
			}
			byFunc[funcName] = append(byFunc[funcName], b)
		}
		if len(byFunc) == 0 {
			continue
		}
		funcNames := make([]string, 0, len(byFunc))
		for fn := range byFunc {
			funcNames = append(funcNames, fn)
		}
		sort.SliceStable(funcNames, func(i, j int) bool {
			// Use the first block's start line of each function
			// so the rendered order is "as the code is read".
			return byFunc[funcNames[i]][0].StartLine <
				byFunc[funcNames[j]][0].StartLine
		})
		fg := fileGroup{Path: path}
		for _, fn := range funcNames {
			fg.Funcs = append(fg.Funcs, funcGroup{Func: fn, Blocks: byFunc[fn]})
		}
		out = append(out, fg)
	}
	return out
}

// filterFile reports whether path should be dropped from the
// report under the default (policy-aware) filter: a path with no
// matching layer is outside the gate's scope, and a path that
// matches any exclude is intentionally exempt.
func filterFile(path string, layers []Layer, excludes []policy.Exclude) bool {
	// Drop files outside the configured layer set.
	if _, ok := longestPrefixLayer(layers, stripPathToLayerPrefix(path)); !ok {
		return true
	}
	if policy.MatchesExclude(path, excludes) {
		return true
	}
	return false
}

// filterFunc reports whether (funcName, path) should be dropped
// under the default filter — the same structural-skip logic the
// gate applies.
func filterFunc(funcName, path string, skips []policy.Skip) bool {
	return policy.MatchesSkip(funcName, path, skips)
}

// stripPathToLayerPrefix converts a file path to the prefix
// [longestPrefixLayer] expects to compare against the configured
// layer paths. The cover output reports `internal/foo/bar.go`;
// the layer config records `internal/foo/...` (with `bar.go`
// stripped). The helper drops everything after the last `/`.
func stripPathToLayerPrefix(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return path
	}
	return path[:idx]
}

// collectUncoveredBlocks deduplicates the merged coverprofile by
// block location (sums execution counts across every appearance,
// the same way `go tool cover -func` does internally) and returns
// only the blocks whose final count is zero. The map is keyed by
// repo-relative file path; entries within a file sort by start
// line so callers can render in source order.
//
// Without the dedup, cross-module `-coverpkg` makes every block
// appear in N per-module profiles — a single uncovered block
// renders N times, and a block covered by one module's tests
// still surfaces N-1 times under "uncovered" because each of the
// other (N-1) profiles records it with count=0.
func collectUncoveredBlocks(merged string, prefixes []modules.Import) map[string][]UncoveredBlock {
	out := map[string][]UncoveredBlock{}
	for _, b := range parseMergedBlocks(merged) {
		if b.Count > 0 {
			continue
		}
		rel := toRepoRelative(prefixes, b.Path)
		out[rel] = append(out[rel], UncoveredBlock{
			StartLine: b.StartLine,
			EndLine:   b.EndLine,
			Stmts:     b.Stmts,
		})
	}
	for _, list := range out {
		sort.SliceStable(list, func(i, j int) bool {
			return list[i].StartLine < list[j].StartLine
		})
	}
	return out
}

// indexFunctionsByFile parses `go tool cover -func` output into a
// per-file sorted list of [funcSpan] entries so [functionAt] can
// look up the containing function for any line. Lines that do
// not match the canonical `<path>:<line>:\t<func>\t<pct>` shape
// (the `total:` row, blanks) are skipped.
func indexFunctionsByFile(funcLog string, prefixes []modules.Import) map[string][]funcSpan {
	out := map[string][]funcSpan{}
	for line := range strings.SplitSeq(funcLog, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "total:" {
			continue
		}
		path, startLine, ok := splitFuncCoverHead(fields[0])
		if !ok {
			continue
		}
		funcName := strings.Join(fields[1:len(fields)-1], " ")
		rel := toRepoRelative(prefixes, path)
		out[rel] = append(out[rel], funcSpan{StartLine: startLine, Func: funcName})
	}
	for _, spans := range out {
		sort.SliceStable(spans, func(i, j int) bool {
			return spans[i].StartLine < spans[j].StartLine
		})
	}
	return out
}

// splitFuncCoverHead pulls the path + start-line out of the
// `<path>:<startline>:` leading token `go tool cover -func`
// emits. The trailing colon is dropped before the line is parsed.
func splitFuncCoverHead(token string) (path string, startLine int, ok bool) {
	token = strings.TrimSuffix(token, ":")
	idx := strings.LastIndex(token, ":")
	if idx <= 0 {
		return "", 0, false
	}
	line, err := strconv.Atoi(token[idx+1:])
	if err != nil {
		return "", 0, false
	}
	return token[:idx], line, true
}

// functionAt returns the name of the function in spans whose
// span covers line — the latest funcSpan with StartLine ≤ line.
// Returns "" when no function spans it (which surfaces in the
// report as a blank function label rather than a hard error).
func functionAt(spans []funcSpan, line int) string {
	best := ""
	for _, sp := range spans {
		if sp.StartLine > line {
			break
		}
		best = sp.Func
	}
	return best
}

// renderUncovered writes the per-file → per-function uncovered
// report followed by a one-line aggregate. Files are sorted
// alphabetically and functions within each file by start line
// so the output is reproducible.
func renderUncovered(w io.Writer, s style.Style, groups []fileGroup) {
	fmt.Fprintln(w)
	if len(groups) == 0 {
		fmt.Fprintf(w, "  %s\n\n",
			s.Dimmed("— every covered function has zero uncovered blocks"))
		s.FinalVerdict(w, true, "no uncovered lines under the active filter")
		fmt.Fprintln(w)
		return
	}
	totalBlocks, totalStmts := 0, 0
	for _, fg := range groups {
		fmt.Fprintf(w, "  %s\n", s.Bolded(fg.Path))
		for _, fn := range fg.Funcs {
			label := fn.Func
			if label == "" {
				label = "(unknown function)"
			}
			fmt.Fprintf(w, "    %s\n", label)
			for _, b := range fn.Blocks {
				fmt.Fprintf(w, "      %d-%d %s\n",
					b.StartLine, b.EndLine,
					s.Dimmed(fmt.Sprintf("(%d stmts)", b.Stmts)))
				totalBlocks++
				totalStmts += b.Stmts
			}
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n",
		s.Dimmed(fmt.Sprintf("%d uncovered block(s) across %d file(s); %d statement(s) total",
			totalBlocks, len(groups), totalStmts)))
	fmt.Fprintln(w)
}
