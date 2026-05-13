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
// `(new - old) / old * 100`; benchstat's own "vs base" column is
// statistically gated and shows "~" for inconclusive comparisons,
// so the percent change is recomputed from the raw values.
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
		results = append(results, Result{
			Bench:        fields[0],
			Metric:       currentMetric,
			Old:          oldVal,
			New:          newVal,
			DeltaPercent: delta,
		})
	}
	if len(results) == 0 && currentMetric == "" {
		return nil, fmt.Errorf("benchstat csv: no metric blocks found in output")
	}
	return results, nil
}

// regressions returns the subset of results whose DeltaPercent
// exceeds the configured threshold for their metric. Negative
// deltas (improvements) and metrics with no threshold configured
// never appear.
func regressions(results []Result, thresholds Thresholds) []Result {
	var out []Result
	for _, r := range results {
		threshold := metricThreshold(r.Metric, thresholds)
		if threshold < 0 {
			continue
		}
		if r.DeltaPercent > threshold {
			out = append(out, r)
		}
	}
	return out
}

// metricThreshold returns the configured threshold for metric, or
// -1 when ergon does not enforce a gate on that metric today.
func metricThreshold(metric string, t Thresholds) float64 {
	switch metric {
	case MetricTime:
		return t.TimePercent
	case MetricBytes:
		return t.BytesPercent
	case MetricAllocs:
		return t.AllocsPercent
	default:
		return -1
	}
}
