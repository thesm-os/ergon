// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"math"
	"testing"
)

// sampleCSV is `benchstat -format csv old.txt new.txt` output
// captured from a hand-rolled pair of benchmark files: two
// benchmarks (A, B), three samples each, baseline at 100/200 ns
// and the new run at 110/300 ns. Used as the parser fixture so
// any future benchstat format change shows up in test diffs.
const sampleCSV = `B7: need >= 6 samples for confidence interval at level 0.95
goos: linux
goarch: amd64
pkg: foo
cpu: Intel
,/tmp/old.txt,,/tmp/new.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
A-8,1.01e-07,∞,1.1e-07,∞,~,p=0.100 n=3
B-8,2e-07,∞,3e-07,∞,~,p=0.100 n=3
geomean,1.42e-07,,1.82e-07,,+27.81%,

,/tmp/old.txt,,/tmp/new.txt,,,
,B/op,CI,B/op,CI,vs base,P
A-8,128,∞,128,∞,~,p=1.000 n=3
B-8,256,∞,320,∞,~,p=0.100 n=3
geomean,181,,202,,+11.80%,

,/tmp/old.txt,,/tmp/new.txt,,,
,allocs/op,CI,allocs/op,CI,vs base,P
A-8,2,∞,2,∞,~,p=1.000 n=3
B-8,4,∞,6,∞,~,p=0.100 n=3
geomean,2.83,,3.46,,+22.47%,
`

// TestParseBenchstatCSV pins the parser against the canonical
// benchstat CSV shape: three metric blocks (sec/op, B/op,
// allocs/op), one row per benchmark per metric, geomean dropped,
// spreadsheet annotations dropped.
func TestParseBenchstatCSV(t *testing.T) {
	t.Parallel()

	t.Run("returns one Result per benchmark per metric", func(t *testing.T) {
		t.Parallel()
		got, err := parseBenchstatCSV(sampleCSV)
		if err != nil {
			t.Fatalf("parseBenchstatCSV err: %v", err)
		}
		// 2 benchmarks * 3 metrics = 6 results.
		if len(got) != 6 {
			t.Fatalf("results = %d, want 6", len(got))
		}
	})

	t.Run("DeltaPercent is computed from raw values not the vs-base column", func(t *testing.T) {
		t.Parallel()
		got, err := parseBenchstatCSV(sampleCSV)
		if err != nil {
			t.Fatalf("parseBenchstatCSV err: %v", err)
		}
		// A sec/op: 1.01e-07 -> 1.1e-07 ≈ +8.9%
		// B sec/op: 2e-07 -> 3e-07 = +50%
		// B B/op: 256 -> 320 = +25%
		// B allocs/op: 4 -> 6 = +50%
		want := map[string]float64{
			"A-8:sec/op":    8.91,
			"B-8:sec/op":    50.0,
			"A-8:B/op":      0.0,
			"B-8:B/op":      25.0,
			"A-8:allocs/op": 0.0,
			"B-8:allocs/op": 50.0,
		}
		for _, r := range got {
			key := r.Bench + ":" + r.Metric
			expected, ok := want[key]
			if !ok {
				t.Errorf("unexpected result %s", key)
				continue
			}
			if math.Abs(r.DeltaPercent-expected) > 0.5 {
				t.Errorf("%s delta = %.2f%%, want ~%.2f%%", key, r.DeltaPercent, expected)
			}
		}
	})

	t.Run("geomean rows are dropped", func(t *testing.T) {
		t.Parallel()
		got, err := parseBenchstatCSV(sampleCSV)
		if err != nil {
			t.Fatalf("parseBenchstatCSV err: %v", err)
		}
		for _, r := range got {
			if r.Bench == "geomean" {
				t.Fatalf("geomean leaked into results: %+v", r)
			}
		}
	})
}

// TestRegressions pins the threshold-application logic: only
// over-threshold positive deltas are returned; improvements and
// untracked metrics are silent.
func TestRegressions(t *testing.T) {
	t.Parallel()

	results := []Result{
		{Bench: "A", Metric: "sec/op", DeltaPercent: 3.0},  // under threshold
		{Bench: "B", Metric: "sec/op", DeltaPercent: 10.0}, // over
		{Bench: "C", Metric: "sec/op", DeltaPercent: -8.0}, // improvement
		{Bench: "D", Metric: "B/op", DeltaPercent: 20.0},   // over
		{Bench: "E", Metric: "unknown", DeltaPercent: 99},  // no threshold
	}
	thresholds := Thresholds{TimePercent: 5, BytesPercent: 5, AllocsPercent: 5}

	got := regressions(results, thresholds)
	if len(got) != 2 {
		t.Fatalf("regressions = %+v, want 2 (B + D)", got)
	}
	names := []string{got[0].Bench, got[1].Bench}
	if names[0] != "B" || names[1] != "D" {
		t.Fatalf("regressed = %+v, want [B, D]", names)
	}
}
