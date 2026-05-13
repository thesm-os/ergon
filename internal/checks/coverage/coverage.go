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

	targets := selectTargets(cfg.Packages, opts.Targets)
	if len(targets) == 0 {
		return fmt.Errorf("coverage: no matching targets for %v", opts.Targets)
	}

	prefixes := sortedPrefixes(imports)
	s := style.Detect(stdout)
	anyFailed := false

	for _, target := range targets {
		if renderTarget(stdout, s, target, rows, excludes, skips, prefixes, cfg.TopN, opts.Verbose, mergedBody) {
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

// selectTargets filters cfg.Packages by the user-supplied
// targets. With no targets every package is selected as declared.
//
// For each requested target, the longest-prefix matching layer
// supplies the threshold; the returned layer's Path narrows to the
// request. This mirrors the per-function classification rule:
// when two layers overlap (e.g. `internal/...` and
// `internal/checks/...`), only the more-specific one applies.
func selectTargets(packages []Layer, requested []string) []Layer {
	if len(requested) == 0 {
		return packages
	}
	var out []Layer
	for _, t := range requested {
		t = strings.TrimSuffix(t, "/")
		match, ok := longestPrefixLayer(packages, t)
		if !ok {
			continue
		}
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
	}
	return out
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
		base := strings.TrimSuffix(p.Path, "/...")
		if base == t || strings.HasPrefix(t, base+"/") {
			if len(base) > bestLen {
				best = p
				bestLen = len(base)
			}
		}
	}
	return best, bestLen >= 0
}

// renderTarget classifies every function under a single layer
// and writes the per-target section to stdout. Returns true when
// at least one function failed the threshold check.
func renderTarget(
	stdout io.Writer, s style.Style, layer Layer,
	rows []funcRow, excludes []policy.Exclude,
	skips []policy.Skip, prefixes []modules.Import, topN int, verbose bool,
	mergedBody string,
) bool {
	prefix := strings.TrimSuffix(layer.Path, "/...")
	s.Header(stdout, prefix, fmt.Sprintf("line ≥ %d%%", layer.Line))

	var (
		total, passing, failing, skipped, excluded int
		failures                                   []Failure
	)
	for _, r := range rows {
		rel := toRepoRelative(prefixes, r.Path)
		if !strings.HasPrefix(rel, prefix+"/") && rel != prefix {
			continue
		}
		total++
		if int(r.Pct) >= layer.Line {
			passing++
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
		failing++
		failures = append(failures, Failure{
			Path: rel, Func: r.Func, Pct: r.Pct, Threshold: layer.Line, Layer: prefix,
		})
	}

	fmt.Fprintf(stdout,
		"  Functions:  %d total · %d ≥ threshold · %d below · %d skipped · %d excluded\n",
		total, passing, failing, skipped, excluded)

	if failing == 0 {
		fmt.Fprintf(stdout, "  Verdict:    %s\n\n", s.Verdict(true))
		return false
	}

	fmt.Fprintf(stdout, "  Verdict:    %s\n\n", s.Verdict(false))

	sort.SliceStable(failures, func(i, j int) bool {
		return failures[i].Pct < failures[j].Pct
	})

	fmt.Fprintf(stdout, "  %s\n", s.Bolded("Functions below threshold"))
	limit := min(topN, len(failures))
	for _, f := range failures[:limit] {
		fmt.Fprintf(stdout, "    %5.1f%%  %s  %s\n", f.Pct, f.Path, s.Dimmed(f.Func))
	}
	if len(failures) > limit {
		fmt.Fprintf(stdout, "    %s\n", s.Dimmed(fmt.Sprintf("… and %d more function(s)", len(failures)-limit)))
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
	for line := range strings.SplitSeq(merged, "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != "0" {
			continue
		}
		path, span, ok := splitProfileHead(fields[0])
		if !ok {
			continue
		}
		rel := toRepoRelative(prefixes, path)
		if !slices.Contains(files, rel) {
			continue
		}
		startEnd := strings.Split(span, ",")
		if len(startEnd) != 2 {
			continue
		}
		startLine := strings.SplitN(startEnd[0], ".", 2)[0]
		endLine := strings.SplitN(startEnd[1], ".", 2)[0]
		fmt.Fprintf(w, "    %s:%s-%s (%s stmts)\n", rel, startLine, endLine, fields[1])
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
					fmt.Fprintln(f, line)
					body.WriteString(line + "\n")
					modeWritten = true
				}
				continue
			}
			if line == "" {
				continue
			}
			fmt.Fprintln(f, line)
			body.WriteString(line + "\n")
		}
	}
	return f.Name(), body.String(), cleanup, nil
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
