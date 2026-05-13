// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package mutation enforces per-layer mutation-testing thresholds
// via the `gremlins` runner. Per-layer thresholds (test efficacy
// and mutator coverage) live inline under `.ergon.yaml`'s
// `checks.mutation.packages` section; invocation policy (workers,
// timeout, exclude-files) lives under `checks.mutation.gremlins`.
package mutation

import "runtime"

// Config declares the per-layer thresholds and gremlins invocation
// policy. An empty [Config.Packages] disables the check —
// `ergon check mutation` short-circuits with a notice rather than
// treating zero layers as a hard fail.
type Config struct {
	// Packages declares the per-layer thresholds.
	Packages []Layer `mapstructure:"packages"`

	// Gremlins controls how ergon invokes the gremlins binary
	// (workers, test-cpu, timeout, exclude-files). gremlins itself
	// has no config file; ergon owns the policy.
	Gremlins GremlinsConfig `mapstructure:"gremlins"`
}

// Layer pairs a path glob with the minimum acceptable mutation
// metrics for every package under that path.
//
// Two metrics gate independently:
//
//   - Score: test efficacy (KILLED / (KILLED + LIVED)). Of the
//     mutants the test suite reached, how many were caught.
//     Catches "tests exercise the path but assert nothing."
//
//   - Coverage: mutator coverage ((KILLED + LIVED) / TOTAL_VIABLE).
//     Of every viable mutant, how many the test suite reached at
//     all. Catches "tests don't reach the mutateable code."
//
// A package can score 100% efficacy on a single covered mutant
// while leaving 99% of the surface untouched — Coverage is the
// gate that catches that. Both must pass.
//
// When Coverage is omitted (zero), the runtime falls back to
// Score so the single-threshold shape stays usable.
type Layer struct {
	// Path is the glob the thresholds apply to. Today only the
	// `<dir>/...` shape is honoured; the runner trims the suffix
	// and uses the resulting directory as gremlins' target.
	Path string `mapstructure:"path"`

	// Score is the minimum test-efficacy percentage. 100 means
	// every reached mutant must be killed.
	Score int `mapstructure:"score"`

	// Coverage is the minimum mutator-coverage percentage. Zero
	// means "default to Score" so the single-threshold shape
	// stays usable.
	Coverage int `mapstructure:"coverage"`
}

// GremlinsConfig carries the per-invocation tuning ergon passes
// to `gremlins unleash`. Field defaults are chosen for CI fairness
// (bounded CPU usage) and to tolerate slow-but-finite tests.
type GremlinsConfig struct {
	// Workers caps the number of parallel mutation workers
	// gremlins runs. gremlins defaults to NumCPU; ergon bounds it
	// to NumCPU/4 (floor 2) so the rest of the developer's session
	// has headroom and back-to-back layer runs do not contend.
	Workers int `mapstructure:"workers"`

	// TestCPU is the `go test -cpu` setting each worker uses. Two
	// keeps a 16-core machine from spawning 256-way parallelism
	// per layer.
	TestCPU int `mapstructure:"test_cpu"`

	// TimeoutCoefficient is gremlins' `--timeout-coefficient`. The
	// gremlins default of 3 is too aggressive for tests that
	// include rapid property-based tests or build-info parsing —
	// slow-but-finite tests time out spuriously and inflate the
	// LIVED count. 30 lets elite tests actually KILL their mutants.
	TimeoutCoefficient int `mapstructure:"timeout_coefficient"`

	// ExcludeFiles is the regex passed to `--exclude-files`.
	// Default skips stringer-generated `_string.go` files;
	// arithmetic-identity mutants on `i - 0` are unkillable there.
	// Repos extend the regex to cover their own generated files.
	ExcludeFiles string `mapstructure:"exclude_files"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the mutation section. The
// Gremlins block is populated; Packages stays empty so the check
// short-circuits until the project declares thresholds.
func Defaults() Config {
	return Config{
		Gremlins: GremlinsConfig{
			Workers:            defaultWorkers(),
			TestCPU:            2,
			TimeoutCoefficient: 30,
			ExcludeFiles:       `.*_string\.go$`,
		},
	}
}

// defaultWorkers returns NumCPU/4 with a floor of 2. The bash
// script uses the same heuristic so a developer who switches from
// the script to ergon sees the same parallelism.
func defaultWorkers() int {
	n := runtime.NumCPU() / 4
	if n < 2 {
		return 2
	}
	return n
}
