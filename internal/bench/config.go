// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package bench implements `ergon bench baseline` and `ergon bench
// regression`. Baseline records a pinned reference suite to
// `bench/baseline.txt` in the benchstat-compatible text format;
// Regression runs the suite again, hands both files to `benchstat`
// for the human-facing diff, and parses `benchstat -format csv` to
// enforce per-metric thresholds.
package bench

// Config carries the per-repository overrides ergon reads from
// `.ergon.yaml`'s `bench` section. Sample count and per-package
// timeout come from `test.Config` (the same knobs `ergon test
// bench` reads); ergon adds the baseline location and the
// regression thresholds.
type Config struct {
	// BaselinePath is the repo-relative path where
	// `ergon bench baseline` writes the pinned reference output.
	// `ergon bench regression` reads it back at compare time.
	BaselinePath string `yaml:"baseline_path"`

	// Thresholds is the per-metric regression gate
	// `ergon bench regression` enforces.
	Thresholds Thresholds `yaml:"thresholds"`
}

// Thresholds expresses the per-metric regression policy. Each
// metric is gated differently; the percentages name the boundary
// each policy applies.
//
// Defaults follow the conventional benchmark-regression policy:
//
//   - TimePercent = 5 — `sec/op` regressions of 5% or more are
//     hard failures when benchstat reports the change as
//     statistically significant.
//
//   - AllocsPercent = 0 — `allocs/op` is a contractual ceiling.
//     Any statistically-significant positive change is a hard
//     failure; the threshold is the minimum delta above which the
//     gate triggers (0 means "any positive").
//
//   - BytesPercent = 10 — `B/op` is advisory. Changes of 10% or
//     more surface as warnings, never as failures. Memory usage
//     shifts under unrelated struct-padding changes too readily
//     for a hard gate.
type Thresholds struct {
	// TimePercent is the hard-fail threshold for the `sec/op`
	// metric. Significant deltas at or above this percent fail
	// the run.
	TimePercent float64 `yaml:"time_percent"`

	// BytesPercent is the advisory threshold for the `B/op` metric.
	// Deltas at or above this percent surface as warnings; the
	// metric is never hard-gated.
	BytesPercent float64 `yaml:"bytes_percent"`

	// AllocsPercent is the hard-fail threshold for the `allocs/op`
	// metric. Significant deltas strictly above this percent fail
	// the run. Defaults to zero — any statistically-significant
	// allocation increase is a regression.
	AllocsPercent float64 `yaml:"allocs_percent"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the bench section.
func Defaults() Config {
	return Config{
		BaselinePath: "bench/baseline.txt",
		Thresholds: Thresholds{
			TimePercent:   5,
			BytesPercent:  10,
			AllocsPercent: 0,
		},
	}
}
