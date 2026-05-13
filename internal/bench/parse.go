// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Result records one (benchmark, metric) pair from the output of
// `benchstat -format csv`. DeltaPercent is computed as
// `(new - old) / old * 100` so the percentage is independent of
// benchstat's text formatting. Significant carries benchstat's own
// statistical verdict from the "vs base" column: false when
// benchstat printed `~` (the change is within noise), true when it
// printed a signed percentage.
type Result struct {
	// Bench is the benchmark name as benchstat reports it,
	// including the `-N` GOMAXPROCS suffix (e.g.
	// `BenchmarkFoo-8`).
	Bench string

	// Metric is one of `sec/op`, `B/op`, `allocs/op` — the three
	// dimensions ergon enforces thresholds on today.
	Metric string

	// Old is the baseline value in the metric's native unit
	// (e.g. seconds for sec/op).
	Old float64

	// New is the fresh-sample value in the same unit.
	New float64

	// DeltaPercent is the percent change from Old to New;
	// positive means a regression (slower / more bytes / more
	// allocs).
	DeltaPercent float64

	// Significant is true when benchstat reported a signed
	// percentage for this comparison rather than the literal `~`.
	// False means the change is within the statistical noise floor
	// for the sample size; time- and alloc-gates short-circuit on
	// false to avoid flapping CI on inconclusive deltas.
	Significant bool
}

// Verdict labels one [Outcome] with how its delta compares to the
// configured threshold. Pass means the change is within budget;
// Warn means the change exceeds an advisory ceiling but does not
// fail the run; Fail means the change exceeds a hard ceiling.
type Verdict int

const (
	// VerdictPass means the change is within the configured
	// threshold (or statistically insignificant). The line still
	// prints in the per-target summary but is not surfaced as a
	// regression.
	VerdictPass Verdict = iota
	// VerdictWarn means the change exceeds an advisory threshold.
	// Reported but does not fail the command — used today for
	// [MetricBytes], whose noise floor under struct-padding
	// changes makes a hard gate impractical.
	VerdictWarn
	// VerdictFail means the change exceeds the configured threshold
	// for a metric ergon hard-gates on (today [MetricTime] and
	// [MetricAllocs]). Surfaces as a regression and fails the run.
	VerdictFail
)

// Outcome pairs a [Result] with its [Verdict] under a given
// [Thresholds] configuration. Returned by [classify] so the
// reporting layer can render the same set of records as both
// failures and warnings.
type Outcome struct {
	Result    Result
	Verdict   Verdict
	Threshold float64
}

// Canonical metric names benchstat emits. Used as both the header
// match in the CSV parser and the keys ergon's [Thresholds]
// dispatch reads.
const (
	MetricTime   = "sec/op"
	MetricBytes  = "B/op"
	MetricAllocs = "allocs/op"
)

// cellAnnotation matches the "B7: ..." spreadsheet hints
// benchstat prepends to CSV output. These rows are noise for
// programmatic consumers and the parser drops them.
var cellAnnotation = regexp.MustCompile(`^[A-Z]\d+:`)

// parseBenchstatCSV turns the output of `benchstat -format csv`
// into a slice of [Result] records. The CSV is structured as a
// preamble (goos / goarch / pkg / cpu) followed by one block per
// metric; each block has a file-header row, a column-header row
// naming the metric, and per-benchmark rows of the form
// `<name>,<old>,<old CI>,<new>,<new CI>,<vs base>,<p>`. The
// `geomean` row is dropped — ergon enforces per-benchmark
// thresholds, not aggregate ones.
func parseBenchstatCSV(out string) ([]Result, error) {
	var results []Result
	currentMetric := ""

	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if cellAnnotation.MatchString(trimmed) {
			continue
		}
		// Preamble lines like `goos: darwin`. Drop them.
		isPreamble := !strings.HasPrefix(trimmed, ",") &&
			!strings.HasPrefix(trimmed, "geomean") &&
			!strings.Contains(trimmed, ",")
		if isPreamble {
			continue
		}

		fields := strings.Split(line, ",")

		// Column-header rows: `,<metric>,CI,<metric>,CI,vs base,P`.
		// benchstat repeats the metric name in column 1 and column 3
		// (once per file). Use column 1 as the canonical name.
		if len(fields) >= 2 && fields[0] == "" {
			switch fields[1] {
			case MetricTime, MetricBytes, MetricAllocs:
				currentMetric = fields[1]
				continue
			}
			// File-header row or other separator we don't care about.
			continue
		}

		if currentMetric == "" {
			continue
		}
		if fields[0] == "geomean" {
			continue
		}
		if len(fields) < 5 {
			continue
		}

		oldVal, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		newVal, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			continue
		}

		delta := 0.0
		if oldVal != 0 {
			delta = (newVal - oldVal) / oldVal * 100
		}
		significant := true
		if len(fields) >= 6 {
			vsBase := strings.TrimSpace(fields[5])
			if vsBase == "" || vsBase == "~" {
				significant = false
			}
		}
		results = append(results, Result{
			Bench:        fields[0],
			Metric:       currentMetric,
			Old:          oldVal,
			New:          newVal,
			DeltaPercent: delta,
			Significant:  significant,
		})
	}
	if len(results) == 0 && currentMetric == "" {
		return nil, fmt.Errorf("benchstat csv: no metric blocks found in output")
	}
	return results, nil
}

// classify applies the per-metric policy to every result and
// returns one [Outcome] per result. Policies:
//
//   - [MetricTime]: fail when [Result.Significant] is true AND
//     DeltaPercent exceeds [Thresholds.TimePercent]. Insignificant
//     changes never fail.
//
//   - [MetricAllocs]: fail when [Result.Significant] is true AND
//     DeltaPercent strictly exceeds [Thresholds.AllocsPercent]
//     (default zero — any statistically-significant positive change
//     is a regression). Allocation counts are contractual ceilings.
//
//   - [MetricBytes]: warn (never fail) when DeltaPercent meets or
//     exceeds [Thresholds.BytesPercent]. Memory usage shifts under
//     unrelated struct-padding changes too readily for a hard gate;
//     ergon surfaces an advisory line instead.
//
// Improvements (negative deltas), zero-delta runs, and unknown
// metrics fall through with [VerdictPass].
func classify(results []Result, t Thresholds) []Outcome {
	out := make([]Outcome, 0, len(results))
	for _, r := range results {
		oc := Outcome{Result: r, Verdict: VerdictPass}
		switch r.Metric {
		case MetricTime:
			oc.Threshold = t.TimePercent
			if r.Significant && r.DeltaPercent >= t.TimePercent {
				oc.Verdict = VerdictFail
			}
		case MetricAllocs:
			oc.Threshold = t.AllocsPercent
			if r.Significant && r.DeltaPercent > t.AllocsPercent {
				oc.Verdict = VerdictFail
			}
		case MetricBytes:
			oc.Threshold = t.BytesPercent
			if r.DeltaPercent >= t.BytesPercent {
				oc.Verdict = VerdictWarn
			}
		}
		out = append(out, oc)
	}
	return out
}

// failures returns the subset of outcomes whose Verdict is
// [VerdictFail]. Convenience wrapper for the reporting layer.
func failures(outcomes []Outcome) []Outcome {
	var out []Outcome
	for _, o := range outcomes {
		if o.Verdict == VerdictFail {
			out = append(out, o)
		}
	}
	return out
}

// warnings returns the subset of outcomes whose Verdict is
// [VerdictWarn]. Convenience wrapper for the reporting layer.
func warnings(outcomes []Outcome) []Outcome {
	var out []Outcome
	for _, o := range outcomes {
		if o.Verdict == VerdictWarn {
			out = append(out, o)
		}
	}
	return out
}
