// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package mutation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.thesmos.sh/ergon/internal/checks/policy"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/style"
)

// RunOptions carries the per-invocation overrides the cobra layer
// passes through to [Run]. Targets restricts which layers run;
// Verbose dumps every non-killed mutant location for the failing
// layers.
type RunOptions struct {
	// Targets is the list of layer prefixes the user wants to
	// exercise. Each entry is either `<layer>` (the layer key as
	// declared in cfg.Packages) or `<layer>/<subpath>` (restrict
	// gremlins to a subdirectory while keeping the layer's
	// thresholds). Empty means "every layer in cfg.Packages".
	Targets []string

	// Verbose, when true, prints every non-killed mutant (LIVED /
	// NOT COVERED / TIMED OUT) verbatim under each failing layer so
	// developers can jump straight to the file:line:col that needs a
	// new test.
	Verbose bool
}

// maxTopFiles caps the contributing-files breakdown printed under
// every failing layer. Anything past the cap collapses into a "…
// and N more file(s)" tail so the report stays scannable.
const maxTopFiles = 10

// Run is `ergon check mutation`: runs `gremlins unleash` against
// each selected layer, parses the resulting Test efficacy / Mutator
// coverage percentages plus per-mutant detail rows, and fails any
// layer below its threshold.
//
// gremlins' own exit code is unreliable — the tool has been
// observed to exit 0 regardless of measured efficacy — so the
// parser owns the verdict.
//
// excludes and skips are the shared [policy.Exclude] and
// [policy.Skip] lists coverage also reads. The path globs in
// excludes and the FileGlob field of every skip are joined into
// the single regex `gremlins --exclude-files` accepts; gremlins
// has no per-function exclusion knob, so a skip's FuncGlob is not
// applied here (the FileGlob is the closest approximation, which
// matches the bash predecessor's policy).
//
// An empty cfg.Packages short-circuits with a notice; the project
// simply has not declared thresholds yet.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, cfg Config,
	excludes []policy.Exclude, skips []policy.Skip, opts RunOptions,
) error {
	if len(cfg.Packages) == 0 {
		s := style.Detect(stdout)
		s.Header(stdout, "mutation", "per-layer score / coverage thresholds")
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "  %s\n", s.Dimmed("— skipped (no thresholds declared in .ergon.yaml)"))
		fmt.Fprintln(stdout)
		return nil
	}
	cfg = withDefaults(cfg)
	excludeRegex := policy.GremlinsExcludeRegex(excludes, skips)

	targets, err := selectTargets(cfg.Packages, opts.Targets)
	if err != nil {
		return err
	}

	s := style.Detect(stdout)
	anyFailed := false
	ran := 0

	for _, t := range targets {
		full := filepath.Join(root, t.Layer)
		if info, err := os.Stat(full); err != nil || !info.IsDir() {
			fmt.Fprintf(stdout, "[%s] skip — directory missing\n", t.Layer)
			continue
		}
		ran++
		failed := renderTarget(ctx, runner, stdout, s, root, t, cfg.Gremlins, excludeRegex, opts.Verbose)
		if failed {
			anyFailed = true
		}
	}

	if ran == 0 {
		return fmt.Errorf("mutation: no targets exercised")
	}

	if anyFailed {
		s.FinalVerdict(stderr, false,
			"one or more layers below score or coverage threshold")
		return fmt.Errorf("mutation: one or more layers below threshold")
	}
	s.FinalVerdict(stdout, true, "every target met its score and coverage thresholds")
	return nil
}

// target is a resolved (layer, threshold, subpath) tuple selectTargets
// produces. label is the human-facing name printed in the per-target
// header; relPath is the filesystem path (relative to the layer
// directory) ergon hands to gremlins.
type target struct {
	// Layer is the top-level module directory (the prefix the
	// layer's thresholds apply to).
	Layer string

	// RelPath is the filesystem path within the layer to mutate.
	// "." mutates the entire layer; "./kernel/fold/" restricts the
	// run to one subtree.
	RelPath string

	// Label is the human-facing target name printed in the per-
	// section header. It is the layer for whole-layer runs and
	// `<layer>/<subpath>` for restricted runs.
	Label string

	// Score is the minimum test-efficacy percentage.
	Score int

	// Coverage is the minimum mutator-coverage percentage. Resolved
	// against the layer's [Layer.Coverage], falling back to Score
	// when zero so the single-threshold shape stays usable.
	Coverage int
}

// selectTargets resolves the user-supplied positional arguments
// against cfg.Packages and returns the ordered list of [target]s
// to run. With no args every layer is selected. Each arg is one of
// `<layer>` (whole layer) or `<layer>/<sub>` (subtree of a layer);
// the layer prefix selects the threshold entry. An arg that does
// not name a declared layer is a hard error.
func selectTargets(packages []Layer, requested []string) ([]target, error) {
	byLayer := make(map[string]Layer, len(packages))
	order := make([]string, 0, len(packages))
	for _, p := range packages {
		layer := layerDir(p.Path)
		byLayer[layer] = p
		order = append(order, layer)
	}

	if len(requested) == 0 {
		out := make([]target, 0, len(packages))
		for _, layer := range order {
			p := byLayer[layer]
			out = append(out, target{
				Layer:    layer,
				RelPath:  ".",
				Label:    layer,
				Score:    p.Score,
				Coverage: resolveCoverage(p),
			})
		}
		return out, nil
	}

	out := make([]target, 0, len(requested))
	for _, raw := range requested {
		head, sub := splitLayerSubpath(raw)
		p, ok := byLayer[head]
		if !ok {
			return nil, fmt.Errorf("mutation: no thresholds entry for layer %q (declared: %s)",
				head, strings.Join(order, ", "))
		}
		rel := "."
		label := head
		if sub != "" {
			rel = "./" + sub + "/"
			label = head + "/" + sub
		}
		out = append(out, target{
			Layer:    head,
			RelPath:  rel,
			Label:    label,
			Score:    p.Score,
			Coverage: resolveCoverage(p),
		})
	}
	return out, nil
}

// splitLayerSubpath divides "layer/sub/path" into ("layer",
// "sub/path"). A bare "layer" yields ("layer", "").
func splitLayerSubpath(s string) (layer, sub string) {
	s = strings.TrimSuffix(s, "/")
	head, tail, _ := strings.Cut(s, "/")
	return head, tail
}

// resolveCoverage returns the effective coverage threshold for a
// layer, defaulting to Score when Coverage is zero. This preserves
// the single-threshold shape — a project that only declares Score
// gets the same gate on both metrics.
func resolveCoverage(l Layer) int {
	if l.Coverage == 0 {
		return l.Score
	}
	return l.Coverage
}

// renderTarget runs gremlins against one target and writes the
// per-target section. Returns true when at least one threshold
// missed.
func renderTarget(
	ctx context.Context, runner xexec.Runner,
	stdout io.Writer, s style.Style,
	root string, t target, gcfg GremlinsConfig,
	excludeRegex string, verbose bool,
) bool {
	details := fmt.Sprintf("score ≥ %d%% / coverage ≥ %d%%   %s",
		t.Score, t.Coverage,
		s.Dimmed(fmt.Sprintf("(workers=%d, test-cpu=%d)", gcfg.Workers, gcfg.TestCPU)))
	s.Header(stdout, t.Label, details)

	start := time.Now()
	out, runErr := runGremlins(ctx, runner, filepath.Join(root, t.Layer), t.RelPath, gcfg, excludeRegex)
	elapsed := time.Since(start)

	score, scoreOK := parsePercent(out, "Test efficacy:")
	coverage, coverageOK := parsePercent(out, "Mutator coverage:")

	if !scoreOK && !coverageOK {
		if runErr != nil {
			fmt.Fprintf(stdout, "  %s   gremlins did not produce metrics: %v\n\n",
				s.Fail(), runErr)
			return true
		}
		fmt.Fprintf(stdout, "  %s   no result (zero covered mutants) %s\n\n",
			s.Dimmed("SKIP"), s.Dimmed(fmt.Sprintf("[%dms]", elapsed.Milliseconds())))
		return false
	}

	counts := parseCounts(out)
	fmt.Fprintf(stdout, "  Mutants:   %d killed · %d lived · %d not covered · %d timed out   %s\n",
		counts.Killed, counts.Lived, counts.NotCovered, counts.TimedOut,
		s.Dimmed(formatElapsed(elapsed)))

	passScore := score >= float64(t.Score)
	passCoverage := coverage >= float64(t.Coverage)
	fmt.Fprintf(stdout, "  Score:     %5.1f%%   (≥ %d%%)   %s\n",
		score, t.Score, verdictMark(s, passScore))
	fmt.Fprintf(stdout, "  Coverage:  %5.1f%%   (≥ %d%%)   %s\n",
		coverage, t.Coverage, verdictMark(s, passCoverage))

	if passScore && passCoverage {
		fmt.Fprintln(stdout)
		return false
	}

	fmt.Fprintln(stdout)
	files := parseMutantFiles(out)
	if len(files) > 0 {
		writeContributingFiles(stdout, s, t.Layer, files)
	}
	if verbose && len(files) > 0 {
		writeNonKilledMutants(stdout, s, out, files)
	}
	return true
}

// runGremlins invokes `gremlins unleash` against dir with the
// filesystem path passed verbatim. Output is captured to a buffer
// — the verdict is parsed from the captured log, not signalled by
// the subprocess exit code (see the package docblock).
//
// excludeRegex is the precomputed `--exclude-files` value derived
// from the shared policy lists; an empty string omits the flag.
func runGremlins(
	ctx context.Context, runner xexec.Runner, dir, relPath string,
	cfg GremlinsConfig, excludeRegex string,
) (string, error) {
	var buf bytes.Buffer
	args := []string{
		"unleash",
		"--timeout-coefficient", strconv.Itoa(cfg.TimeoutCoefficient),
		"--workers", strconv.Itoa(cfg.Workers),
		"--test-cpu", strconv.Itoa(cfg.TestCPU),
	}
	if excludeRegex != "" {
		args = append(args, "--exclude-files", excludeRegex)
	}
	args = append(args, relPath)
	err := runner.Run(ctx,
		xexec.Options{Dir: dir, Stdout: &buf, Stderr: &buf},
		"gremlins", args...)
	return buf.String(), err
}

// parsePercent scans out for the most recent line whose trimmed
// form starts with prefix and returns the floating-point percentage
// that follows. The second return is false when no such line was
// found — the caller distinguishes "0% measured" from "metric
// missing".
func parsePercent(out, prefix string) (float64, bool) {
	var (
		value float64
		found bool
	)
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		rest = strings.TrimSuffix(rest, "%")
		f, parseErr := strconv.ParseFloat(rest, 64)
		if parseErr != nil {
			continue
		}
		value = f
		found = true
	}
	return value, found
}

// mutantCounts groups the four classifications the per-target
// report surfaces. gremlins prints additional buckets (Not viable,
// Skipped) that do not affect the verdict and are omitted.
type mutantCounts struct {
	Killed     int
	Lived      int
	NotCovered int
	TimedOut   int
}

// countPattern matches the `Killed: N, Lived: N, Not covered: N`
// and `Timed out: N, Not viable: N, Skipped: N` summary lines
// gremlins emits at the end of a run.
var countPattern = regexp.MustCompile(`(?i)(Killed|Lived|Not covered|Timed out):\s*(\d+)`)

// parseCounts extracts the four mutant counts the per-target
// summary line displays. Missing fields stay at zero — gremlins
// omits a class only when there were no mutants of that class.
func parseCounts(out string) mutantCounts {
	var c mutantCounts
	for _, m := range countPattern.FindAllStringSubmatch(out, -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		switch strings.ToLower(m[1]) {
		case "killed":
			c.Killed = n
		case "lived":
			c.Lived = n
		case "not covered":
			c.NotCovered = n
		case "timed out":
			c.TimedOut = n
		}
	}
	return c
}

// fileBreakdown records the per-file contribution to a layer's
// threshold miss. Total is the sum of non-KILLED mutants in the
// file; the individual counts let the report explain *why* the
// file landed on the offender list.
type fileBreakdown struct {
	Path       string
	Total      int
	Lived      int
	NotCovered int
	TimedOut   int
}

// Status tokens gremlins prints in the leading column of each
// mutant-detail row. KILLED rows are not consumed by the parser —
// only the three non-killed classes contribute to the breakdown.
const (
	statusLived      = "LIVED"
	statusNotCovered = "NOT COVERED"
	statusTimedOut   = "TIMED OUT"
)

// mutantLinePattern matches the detail rows gremlins prints for
// every non-KILLED mutant. The shape is:
//
//	<status> <mutator> at <path>:<line>:<col>
//
// where <status> is LIVED, "NOT COVERED", or "TIMED OUT". Leading
// whitespace is gremlins' right-alignment of the status column.
var mutantLinePattern = regexp.MustCompile(
	`^\s*(LIVED|NOT COVERED|TIMED OUT)\s+(\S+)\s+at\s+(\S+?):\d+:\d+\s*$`)

// parseMutantFiles walks gremlins' output, groups every non-killed
// mutant by file, and returns the per-file totals sorted by
// descending total. Files that produced no non-killed mutants do
// not appear.
func parseMutantFiles(out string) []fileBreakdown {
	byPath := map[string]*fileBreakdown{}
	for line := range strings.SplitSeq(out, "\n") {
		m := mutantLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		status, path := m[1], m[3]
		fb, ok := byPath[path]
		if !ok {
			fb = &fileBreakdown{Path: path}
			byPath[path] = fb
		}
		fb.Total++
		switch status {
		case statusLived:
			fb.Lived++
		case statusNotCovered:
			fb.NotCovered++
		case statusTimedOut:
			fb.TimedOut++
		}
	}
	out2 := make([]fileBreakdown, 0, len(byPath))
	for _, fb := range byPath {
		out2 = append(out2, *fb)
	}
	sort.SliceStable(out2, func(i, j int) bool {
		if out2[i].Total != out2[j].Total {
			return out2[i].Total > out2[j].Total
		}
		return out2[i].Path < out2[j].Path
	})
	return out2
}

// writeContributingFiles prints the top-N contributing files
// section under a failing target. The "+N more files" tail keeps
// the report scannable when many files contribute.
func writeContributingFiles(
	w io.Writer, s style.Style, layer string, files []fileBreakdown,
) {
	fmt.Fprintf(w, "  %s   %s\n",
		s.Bolded("Contributing files"),
		s.Dimmed("(L=lived / NC=not covered / TO=timed out)"))
	limit := min(maxTopFiles, len(files))
	for _, fb := range files[:limit] {
		fmt.Fprintf(w, "    %4d   %s/%s   %s\n",
			fb.Total, layer, fb.Path,
			s.Dimmed(fmt.Sprintf("(L:%d / NC:%d / TO:%d)",
				fb.Lived, fb.NotCovered, fb.TimedOut)))
	}
	if more := len(files) - limit; more > 0 {
		fmt.Fprintf(w, "    %s\n", s.Dimmed(fmt.Sprintf("… and %d more file(s)", more)))
	}
	fmt.Fprintln(w)
}

// writeNonKilledMutants dumps every non-killed mutant verbatim,
// grouped by the contributing-files order. The output is shaped so
// editors can jump to file:line:col directly.
func writeNonKilledMutants(
	w io.Writer, s style.Style, gremlinsOut string, files []fileBreakdown,
) {
	fmt.Fprintf(w, "  %s   %s\n",
		s.Bolded("Non-killed mutants"),
		s.Dimmed("(use these locations to extend the test suite)"))

	paths := make(map[string]struct{}, len(files))
	for _, fb := range files {
		paths[fb.Path] = struct{}{}
	}

	type mutantRow struct {
		Status  string
		Mutator string
		Loc     string
	}
	rows := map[string][]mutantRow{}
	for line := range strings.SplitSeq(gremlinsOut, "\n") {
		m := mutantLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		status, mutator, path := m[1], m[2], m[3]
		if _, ok := paths[path]; !ok {
			continue
		}
		_, loc, _ := strings.Cut(line, "at ")
		rows[path] = append(rows[path], mutantRow{
			Status:  status,
			Mutator: mutator,
			Loc:     strings.TrimSpace(loc),
		})
	}

	for _, fb := range files {
		for _, r := range rows[fb.Path] {
			color := s.Dimmed
			if r.Status == statusLived {
				color = s.Bolded
			}
			fmt.Fprintf(w, "    %s  %-26s  %s\n",
				color(fmt.Sprintf("%-3s", shortStatus(r.Status))), r.Mutator, r.Loc)
		}
	}
	fmt.Fprintln(w)
}

// shortStatus is the two-letter tag the verbose dump prefixes each
// mutant line with — kept short so the mutator name and location
// stay in the first 80 columns.
func shortStatus(status string) string {
	switch status {
	case statusLived:
		return "L"
	case statusNotCovered:
		return "NC"
	case statusTimedOut:
		return "TO"
	}
	return "?"
}

// verdictMark renders the right-edge ✓ PASS / ✗ FAIL badge each
// Score / Coverage line ends with.
func verdictMark(s style.Style, pass bool) string {
	if pass {
		return s.Pass()
	}
	return s.Fail()
}

// formatElapsed mirrors the bash script's `[12.3s]` cadence for
// runs longer than one second and `[123ms]` for shorter runs.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("[%dms]", d.Milliseconds())
	}
	secs := float64(d.Milliseconds()) / 1000.0
	return fmt.Sprintf("[%.1fs]", secs)
}

// withDefaults fills any zero-value gremlins field on cfg from
// [Defaults]. Per-layer thresholds are not defaulted; the project
// declares them explicitly.
func withDefaults(cfg Config) Config {
	d := Defaults()
	if cfg.Gremlins.Workers == 0 {
		cfg.Gremlins.Workers = d.Gremlins.Workers
	}
	if cfg.Gremlins.TestCPU == 0 {
		cfg.Gremlins.TestCPU = d.Gremlins.TestCPU
	}
	if cfg.Gremlins.TimeoutCoefficient == 0 {
		cfg.Gremlins.TimeoutCoefficient = d.Gremlins.TimeoutCoefficient
	}
	return cfg
}

// layerDir converts a layer glob like `foo/...` to the directory
// `foo`. Only the `<dir>/...` shape is supported; arbitrary globs
// would require a recursive walk gremlins itself does not perform.
func layerDir(glob string) string {
	return strings.TrimSuffix(glob, "/...")
}
