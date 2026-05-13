// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package bench implements `ergon bench baseline` and `ergon bench
// regression`. Baseline records a pinned reference suite to
// `bench/baseline.txt`; Regression runs the suite again and uses
// `benchstat` to compare the new sample against the pinned
// baseline.
package bench

// Config carries the per-repository overrides ergon reads from
// `.ergon.yaml`'s `bench` section. Sample count and per-package
// timeout come from `test.Config` (the same knobs `ergon test
// bench` reads) so a single tuning surface drives every benchmark
// invocation.
type Config struct {
	// BaselinePath is the repo-relative path where
	// `ergon bench baseline` writes the pinned reference output.
	// `ergon bench regression` reads it back at compare time.
	BaselinePath string `mapstructure:"baseline_path"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the bench section.
func Defaults() Config {
	return Config{
		BaselinePath: "bench/baseline.txt",
	}
}
