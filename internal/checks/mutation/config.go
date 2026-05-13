// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package mutation declares the schema for `ergon check mutation`.
// Per-layer mutation thresholds (test efficacy + mutator coverage)
// live inline under `.ergon.yaml`'s `checks.mutation` section.
//
// The runner (gremlins wrapper) is not implemented yet; this
// package captures the schema so the configuration surface is
// stable while the runner ships separately.
package mutation

// Config declares the per-layer mutation thresholds. An empty
// config disables the check — `ergon check mutation` short-circuits
// with a notice rather than treating zero layers as a hard fail.
type Config struct {
	// Packages declares the per-layer thresholds.
	Packages []Layer `mapstructure:"packages"`
}

// Layer pairs a path glob with the minimum acceptable mutation
// metrics for every function under that path.
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
type Layer struct {
	// Path is the glob the thresholds apply to (`...` recursive,
	// `*` single segment).
	Path string `mapstructure:"path"`

	// Score is the minimum test-efficacy percentage. 100 means
	// every reached mutant must be killed.
	Score int `mapstructure:"score"`

	// Coverage is the minimum mutator-coverage percentage. When
	// omitted, defaults to [Layer.Score] at validation time so the
	// single-threshold shape stays usable.
	Coverage int `mapstructure:"coverage"`
}

// Defaults returns an empty Config. Mutation thresholds are an
// explicit project policy — there is no useful baseline ergon can
// supply.
func Defaults() Config {
	return Config{}
}
