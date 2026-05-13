// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package test implements `ergon test` and its subcommands. The
// package wraps `go test` with the same knobs the Makefile
// templates exposed (cpu, count, timeout, ...) and adds the
// per-module iteration plus fuzz-target discovery the multi-module
// repos in the ecosystem need.
package test

import "time"

// Config carries the per-repository knobs `ergon test` and its
// subcommands pass to `go test`. Field semantics mirror the
// same-named flags so users keep their mental model from the
// Makefile world intact.
type Config struct {
	// CPU bounds the parallelism `go test -cpu=N` exposes to each
	// package. Higher values exercise more concurrent paths; lower
	// values reduce flakiness on noisy CI runners.
	CPU int `mapstructure:"cpu"`

	// Count repeats each test the given number of times via
	// `go test -count=N`. Useful for flushing out flakes that pass
	// on the first iteration.
	Count int `mapstructure:"count"`

	// Timeout is the per-package deadline (`go test -timeout=...`).
	// Stops one runaway test from hanging the whole suite.
	Timeout time.Duration `mapstructure:"timeout"`

	// RaceCount is the iteration count for `ergon test race`. Race
	// tests benefit from extra repetitions because reorderings the
	// detector can observe are stochastic.
	RaceCount int `mapstructure:"race_count"`

	// BenchCount is the per-target run count for `ergon bench`
	// (and `ergon test bench` when sampling). More samples tighten
	// benchstat's confidence bands at the cost of CI time.
	BenchCount int `mapstructure:"bench_count"`

	// FuzzTime is the per-target wall-clock budget for
	// `ergon test fuzz`. Each Fuzz* target runs sequentially for
	// FuzzTime before the runner moves on.
	FuzzTime time.Duration `mapstructure:"fuzz_time"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the test section. The baseline
// numbers mirror the values the Makefile templates have converged
// on (TEST_CPU=4, TEST_COUNT=3, TEST_TIMEOUT=10m, ...).
func Defaults() Config {
	return Config{
		CPU:        4,
		Count:      3,
		Timeout:    10 * time.Minute,
		RaceCount:  3,
		BenchCount: 6,
		FuzzTime:   30 * time.Second,
	}
}

// Override carries the per-invocation CLI flag values the cobra
// layer passes through to each `test` subcommand. Zero values
// mean "use the [Config] default"; non-zero values shadow the
// configured field for this run only.
//
// Pattern has no config equivalent — it lives here because it is
// intrinsically per-invocation (which tests / benchmarks / fuzz
// targets you want to exercise *right now*). The other fields
// mirror the same-named [Config] field, except Count which maps
// to the per-command count (Count / RaceCount / BenchCount).
type Override struct {
	// Pattern is the `-run` regex (for Run / Race), the `-bench`
	// regex (for Bench), or the Fuzz-target name regex (for
	// Fuzz). Empty means: use the per-command default (no -run
	// for Run / Race; "." for Bench; match-every-target for
	// Fuzz).
	Pattern string

	// Count overrides the per-command count (Count, RaceCount, or
	// BenchCount). Zero leaves the configured value in place.
	Count int

	// CPU overrides [Config.CPU]. Zero leaves the configured
	// value in place.
	CPU int

	// Timeout overrides [Config.Timeout]. Zero leaves the
	// configured value in place.
	Timeout time.Duration

	// Time overrides [Config.FuzzTime] for `ergon test fuzz`
	// or feeds `-benchtime` for `ergon test bench`. Zero leaves
	// the configured value in place (and omits `-benchtime`
	// entirely for Bench, which makes Go use its own 1s default).
	Time time.Duration
}
