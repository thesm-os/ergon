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
	BaselinePath string `mapstructure:"baseline_path"`

	// Thresholds is the per-metric regression gate
	// `ergon bench regression` enforces.
	Thresholds Thresholds `mapstructure:"thresholds"`
}

// Thresholds expresses the maximum acceptable per-metric percent
// change a fresh benchmark sample may show against the baseline.
// Anything above is reported as a regression and fails the
// command.
//
// Defaults are calibrated for routine CI use: a 5% time
// regression is the conventional alarm threshold; allocation
// counts and byte counts are also gated at 5%.
type Thresholds struct {
	// TimePercent gates the `sec/op` metric.
	TimePercent float64 `mapstructure:"time_percent"`

	// BytesPercent gates the `B/op` metric.
	BytesPercent float64 `mapstructure:"bytes_percent"`

	// AllocsPercent gates the `allocs/op` metric.
	AllocsPercent float64 `mapstructure:"allocs_percent"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the bench section.
func Defaults() Config {
	return Config{
		BaselinePath: "bench/baseline.txt",
		Thresholds: Thresholds{
			TimePercent:   5,
			BytesPercent:  5,
			AllocsPercent: 5,
		},
	}
}
