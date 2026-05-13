// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package coverage enforces per-layer Go test coverage thresholds.
// The thresholds live inline under `.ergon.yaml`'s
// `checks.coverage` section; the path-exclusion and structural-
// skip rules live one level up under `checks.excludes` /
// `checks.skips` because mutation reads the same rules
// (see [go.thesmos.sh/ergon/internal/checks/policy]).
//
// ergon merges every per-module `.out` profile produced by
// `ergon test`, runs `go tool cover -func` against the result,
// and fails any function below its layer's `line` threshold.
package coverage

// Config declares the per-layer thresholds and the reporting cap.
// Exclude and Skip rules are NOT held here — they live under
// `checks.excludes` / `checks.skips` so mutation can read the
// same set. An empty [Config.Packages] disables the check —
// `ergon check coverage` short-circuits with a notice.
type Config struct {
	// Packages declares the per-layer thresholds. Each entry's
	// [Layer.Path] is a glob (`...` recursive, `*` single segment)
	// matched against the repo-relative path of every function in
	// the merged coverprofile. Longest-prefix wins.
	Packages []Layer `mapstructure:"packages"`

	// TopN caps the per-target failing-function list so the
	// report stays scannable. A surplus is summarised as
	// "+N more functions". Defaults to 10.
	TopN int `mapstructure:"top_n"`
}

// Layer pairs a path glob with the minimum statement-coverage
// percentage every function under that path must reach.
//
// Branch coverage is modelled in the schema (Branch +
// RequireBranch) but not yet enforced — `go test -cover` only
// reports statement coverage. A future change can wire `gobco`
// behind RequireBranch.
type Layer struct {
	// Path is the glob the threshold applies to.
	Path string `mapstructure:"path"`

	// Line is the minimum statement-coverage percentage every
	// function in this layer must reach.
	Line int `mapstructure:"line"`

	// Branch is the minimum branch-coverage percentage. Reserved
	// for the future `gobco`-backed gate; ignored today.
	Branch int `mapstructure:"branch"`

	// RequireBranch turns the branch gate on when the runner
	// supports it. Today the field is recorded but unused.
	RequireBranch bool `mapstructure:"require_branch"`
}

// Defaults returns an empty Config. Coverage thresholds are an
// explicit project policy — there is no useful baseline ergon can
// supply that wouldn't be wrong for most repositories.
func Defaults() Config {
	return Config{
		TopN: 10,
	}
}
