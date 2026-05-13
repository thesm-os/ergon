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
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// Run is `ergon check coverage`: merges every per-module `.out`
// profile under coverageDir, invokes `go tool cover -func`, and
// fails any function whose coverage falls below its layer's
// threshold.
//
// modulePrefix is the import-path prefix the caller strips from
// `go tool cover` output to derive repo-relative paths that match
// the schema's globs (typically `<importPath>/` from
// `discover.ImportPath`).
//
// An empty cfg.Packages short-circuits with a notice — the
// project simply has no thresholds declared yet.
func Run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root, coverageDir, modulePrefix string, cfg Config,
) error {
	if len(cfg.Packages) == 0 {
		fmt.Fprintln(stdout, "coverage: no thresholds declared in .ergon.yaml; skipping")
		return nil
	}

	profiles, err := findProfiles(coverageDir)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return fmt.Errorf("no coverprofiles found in %s — run `ergon test` first", coverageDir)
	}

	mergedPath, cleanup, err := mergeProfiles(profiles)
	if err != nil {
		return err
	}
	defer cleanup()

	funcLog, err := captureFuncCoverage(ctx, runner, root, mergedPath)
	if err != nil {
		return fmt.Errorf("go tool cover -func: %w", err)
	}

	rows := parseFuncLog(funcLog)
	layers := compileLayers(cfg.Packages)
	excludes := compileExcludes(cfg.Excludes)

	report := classify(rows, layers, excludes, modulePrefix)
	printReport(stdout, stderr, report)
	if len(report.Failures) > 0 {
		return fmt.Errorf("%d function(s) below threshold", len(report.Failures))
	}
	return nil
}

// findProfiles returns every `*.out` file under dir, sorted by
// name. An empty result is not an error; callers decide whether
// that's a precondition failure.
func findProfiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
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
// temp file, dropping all but the first `mode:` line so the
// result remains a valid coverprofile. Returns the path and a
// cleanup function the caller defers.
func mergeProfiles(paths []string) (string, func(), error) {
	f, err := os.CreateTemp("", "ergon-cov-*.out")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp: %w", err)
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	modeWritten := false
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("read %s: %w", p, err)
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			if strings.HasPrefix(line, "mode:") {
				if !modeWritten {
					fmt.Fprintln(f, line)
					modeWritten = true
				}
				continue
			}
			if line == "" {
				continue
			}
			fmt.Fprintln(f, line)
		}
	}
	return f.Name(), cleanup, nil
}

// captureFuncCoverage runs `go tool cover -func <profile>` and
// returns the captured stdout. The command produces one line per
// function plus a `total:` row at the end.
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
// of [funcRow]. The `total:` summary row is dropped. Lines that
// do not parse as <path>:<line>: <func> <pct>% are skipped.
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
// tool cover -func` path token. Input `pkg/foo.go:42:` returns
// `pkg/foo.go`.
func stripFileLocation(s string) string {
	s = strings.TrimSuffix(s, ":")
	if idx := strings.LastIndex(s, ":"); idx > 0 {
		return s[:idx]
	}
	return s
}

// compiledLayer pairs a [Layer] with its compiled regex.
type compiledLayer struct {
	Path      string
	Threshold int
	Pattern   *regexp.Regexp
}

// compileLayers translates each schema layer's glob into an
// anchored regex and sorts the result by descending path length —
// the longest-prefix layer wins when multiple match.
func compileLayers(layers []Layer) []compiledLayer {
	out := make([]compiledLayer, 0, len(layers))
	for _, l := range layers {
		out = append(out, compiledLayer{
			Path:      l.Path,
			Threshold: l.Line,
			Pattern:   regexp.MustCompile(globToRegex(l.Path)),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].Path) > len(out[j].Path)
	})
	return out
}

// compileExcludes turns the schema's exclude entries into compiled
// regexes.
func compileExcludes(ex []Exclude) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(ex))
	for _, e := range ex {
		out = append(out, regexp.MustCompile(globToRegex(e.Path)))
	}
	return out
}

// globToRegex translates the schema's glob syntax to an anchored
// regex. `...` matches any sequence of path segments; `*` matches
// within one segment (modelled as `.*` for simplicity).
func globToRegex(pat string) string {
	escaped := strings.ReplaceAll(pat, ".", `\.`)
	triple := strings.ReplaceAll(escaped, `\.\.\.`, ".*")
	star := strings.ReplaceAll(triple, "*", ".*")
	return "^" + star + "$"
}

// Failure records one threshold violation for reporting.
type Failure struct {
	Path      string
	Func      string
	Pct       float64
	Threshold int
	Layer     string
}

// Report bundles the per-run summary the printer formats.
type Report struct {
	Failures []Failure
	Passing  int
	Excluded int
	Unscoped int
}

// classify walks every funcRow and decides whether it passes its
// layer's threshold, is excluded, or fails. Functions outside any
// layer's glob are counted under [Report.Unscoped] but do not fail
// the run — the schema's authors choose what coverage applies to.
func classify(
	rows []funcRow, layers []compiledLayer,
	excludes []*regexp.Regexp, modulePrefix string,
) Report {
	var r Report
	for _, fr := range rows {
		rel := strings.TrimPrefix(fr.Path, modulePrefix)
		if matchesAny(rel, excludes) {
			r.Excluded++
			continue
		}
		layer, threshold, ok := layerFor(rel, layers)
		if !ok {
			r.Unscoped++
			continue
		}
		if int(fr.Pct) >= threshold {
			r.Passing++
			continue
		}
		r.Failures = append(r.Failures, Failure{
			Path:      rel,
			Func:      fr.Func,
			Pct:       fr.Pct,
			Threshold: threshold,
			Layer:     layer,
		})
	}
	sort.SliceStable(r.Failures, func(i, j int) bool {
		return r.Failures[i].Pct < r.Failures[j].Pct
	})
	return r
}

// layerFor returns the longest-prefix layer matching rel.
func layerFor(rel string, layers []compiledLayer) (string, int, bool) {
	for _, l := range layers {
		if l.Pattern.MatchString(rel) {
			return l.Path, l.Threshold, true
		}
	}
	return "", 0, false
}

// matchesAny reports whether s matches any of the regexes.
func matchesAny(s string, patterns []*regexp.Regexp) bool {
	return slices.ContainsFunc(patterns, func(p *regexp.Regexp) bool {
		return p.MatchString(s)
	})
}

// printReport writes the per-run summary to stdout and the
// per-failure list to stderr.
func printReport(stdout, stderr io.Writer, r Report) {
	fmt.Fprintf(stdout, "Coverage: %d passing, %d excluded, %d unscoped, %d failing\n",
		r.Passing, r.Excluded, r.Unscoped, len(r.Failures))
	if len(r.Failures) == 0 {
		return
	}
	fmt.Fprintln(stderr, "Functions below threshold:")
	for _, f := range r.Failures {
		fmt.Fprintf(stderr, "  %5.1f%% < %d%%  %s  %s  (layer %s)\n",
			f.Pct, f.Threshold, f.Path, f.Func, f.Layer)
	}
}
