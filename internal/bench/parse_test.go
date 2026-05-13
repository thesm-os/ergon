// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"math"
	"testing"
)

// sampleCSV is `benchstat -format csv old.txt new.txt` output
// captured from a hand-rolled pair of benchmark files. Every "vs
// base" column is `~` (within noise), so it exercises the
// significance gate: even with large raw deltas, the time and
// alloc gates suppress these as inconclusive.
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

// significantCSV captures the same shape with statistically
// significant "vs base" entries (signed percent in column 5). Used
// by [TestClassify] to exercise the path where the time / alloc
// gates fire.
const significantCSV = `goos: linux
goarch: amd64
pkg: foo
cpu: Intel
,/tmp/old.txt,,/tmp/new.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
A-8,1.0e-07,1%,1.5e-07,1%,+50.00%,p=0.001 n=10
B-8,2.0e-07,1%,2.04e-07,1%,+2.00%,p=0.001 n=10
geomean,1.41e-07,,1.75e-07,,+24.0%,

,/tmp/old.txt,,/tmp/new.txt,,,
,B/op,CI,B/op,CI,vs base,P
A-8,100,1%,120,1%,+20.00%,p=0.001 n=10
geomean,100,,120,,+20.0%,

,/tmp/old.txt,,/tmp/new.txt,,,
,allocs/op,CI,allocs/op,CI,vs base,P
A-8,4,1%,5,1%,+25.00%,p=0.001 n=10
B-8,4,1%,4,1%,~,p=1.000 n=10
geomean,4,,4.47,,+11.8%,
`

// TestParseBenchstatCSV pins the parser against the canonical
// benchstat CSV shape: three metric blocks (sec/op, B/op,
// allocs/op), one row per benchmark per metric, geomean dropped,
// spreadsheet annotations dropped, and the "vs base" column
// projected onto [Result.Significant].
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

	t.Run("vs-base column `~` maps to Significant=false", func(t *testing.T) {
		t.Parallel()
		got, err := parseBenchstatCSV(sampleCSV)
		if err != nil {
			t.Fatalf("parseBenchstatCSV err: %v", err)
		}
		for _, r := range got {
			if r.Significant {
				t.Fatalf("%s %s: Significant=true under all-`~` fixture", r.Bench, r.Metric)
			}
		}
	})

	t.Run("vs-base column with signed percent maps to Significant=true", func(t *testing.T) {
		t.Parallel()
		got, err := parseBenchstatCSV(significantCSV)
		if err != nil {
			t.Fatalf("parseBenchstatCSV err: %v", err)
		}
		// One entry in significantCSV has `~` (B-8 allocs/op);
		// every other row is significant.
		var sigCount, insigCount int
		for _, r := range got {
			if r.Significant {
				sigCount++
			} else {
				insigCount++
			}
		}
		if sigCount == 0 || insigCount == 0 {
			t.Fatalf("sig=%d insig=%d, want a mix", sigCount, insigCount)
		}
	})
}

// TestClassify pins the per-metric policy: sec/op fails on
// significant deltas ≥ TimePercent; allocs/op fails on significant
// strict-positive deltas above AllocsPercent; B/op produces only
// warnings; improvements and insignificant changes never fail.
func TestClassify(t *testing.T) {
	t.Parallel()

	thresholds := Thresholds{TimePercent: 5, BytesPercent: 10, AllocsPercent: 0}

	t.Run("significant sec/op above threshold fails", func(t *testing.T) {
		t.Parallel()
		got := classify([]Result{
			{Bench: "A", Metric: MetricTime, DeltaPercent: 10, Significant: true},
		}, thresholds)
		if got[0].Verdict != VerdictFail {
			t.Fatalf("verdict = %v, want fail", got[0].Verdict)
		}
	})

	t.Run("insignificant sec/op above threshold passes (noise gate)", func(t *testing.T) {
		t.Parallel()
		got := classify([]Result{
			{Bench: "A", Metric: MetricTime, DeltaPercent: 10, Significant: false},
		}, thresholds)
		if got[0].Verdict != VerdictPass {
			t.Fatalf("verdict = %v, want pass — insignificant deltas must not fail", got[0].Verdict)
		}
	})

	t.Run("significant sec/op below threshold passes", func(t *testing.T) {
		t.Parallel()
		got := classify([]Result{
			{Bench: "A", Metric: MetricTime, DeltaPercent: 3, Significant: true},
		}, thresholds)
		if got[0].Verdict != VerdictPass {
			t.Fatalf("verdict = %v, want pass", got[0].Verdict)
		}
	})

	t.Run("significant allocs/op positive delta fails (zero default = strict)", func(t *testing.T) {
		t.Parallel()
		got := classify([]Result{
			{Bench: "A", Metric: MetricAllocs, DeltaPercent: 0.01, Significant: true},
		}, thresholds)
		if got[0].Verdict != VerdictFail {
			t.Fatalf("verdict = %v, want fail — allocs are ceilings", got[0].Verdict)
		}
	})

	t.Run("insignificant allocs/op positive delta passes", func(t *testing.T) {
		t.Parallel()
		got := classify([]Result{
			{Bench: "A", Metric: MetricAllocs, DeltaPercent: 25, Significant: false},
		}, thresholds)
		if got[0].Verdict != VerdictPass {
			t.Fatalf("verdict = %v, want pass — significance gate", got[0].Verdict)
		}
	})

	t.Run("B/op above threshold warns and never fails", func(t *testing.T) {
		t.Parallel()
		got := classify([]Result{
			{Bench: "A", Metric: MetricBytes, DeltaPercent: 50, Significant: true},
		}, thresholds)
		if got[0].Verdict != VerdictWarn {
			t.Fatalf("verdict = %v, want warn — B/op is advisory", got[0].Verdict)
		}
	})

	t.Run("improvements (negative deltas) never fail or warn", func(t *testing.T) {
		t.Parallel()
		got := classify([]Result{
			{Bench: "A", Metric: MetricTime, DeltaPercent: -20, Significant: true},
			{Bench: "A", Metric: MetricAllocs, DeltaPercent: -10, Significant: true},
			{Bench: "A", Metric: MetricBytes, DeltaPercent: -30, Significant: true},
		}, thresholds)
		for _, o := range got {
			if o.Verdict != VerdictPass {
				t.Errorf("%s improvement classified as %v, want pass", o.Result.Metric, o.Verdict)
			}
		}
	})

	t.Run("unknown metrics fall through as pass", func(t *testing.T) {
		t.Parallel()
		got := classify([]Result{
			{Bench: "A", Metric: "unknown", DeltaPercent: 99, Significant: true},
		}, thresholds)
		if got[0].Verdict != VerdictPass {
			t.Fatalf("verdict = %v, want pass for unknown metric", got[0].Verdict)
		}
	})
}

// TestFailures pins the failures filter.
func TestFailures(t *testing.T) {
	t.Parallel()

	got := failures([]Outcome{
		{Verdict: VerdictPass},
		{Verdict: VerdictFail, Result: Result{Bench: "X"}},
		{Verdict: VerdictWarn},
		{Verdict: VerdictFail, Result: Result{Bench: "Y"}},
	})
	if len(got) != 2 || got[0].Result.Bench != "X" || got[1].Result.Bench != "Y" {
		t.Fatalf("failures = %+v, want [X, Y]", got)
	}
}

// TestWarnings pins the warnings filter.
func TestWarnings(t *testing.T) {
	t.Parallel()

	got := warnings([]Outcome{
		{Verdict: VerdictPass},
		{Verdict: VerdictWarn, Result: Result{Bench: "X"}},
		{Verdict: VerdictFail},
	})
	if len(got) != 1 || got[0].Result.Bench != "X" {
		t.Fatalf("warnings = %+v, want [X]", got)
	}
}
