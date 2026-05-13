// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package coverage enforces per-layer Go test coverage thresholds.
// The thresholds and exclude list live inline under
// `.ergon.yaml`'s `checks.coverage` section; ergon merges every
// per-module `.out` profile produced by `ergon test`, runs
// `go tool cover -func` against the result, and fails any
// function below its layer's `line` threshold.
package coverage

// Config declares the per-layer thresholds, exclude list, and
// structural skip rules. An empty config disables the check —
// `ergon check coverage` short-circuits with a notice.
type Config struct {
	// Packages declares the per-layer thresholds. Each entry's
	// [Layer.Path] is a glob (`...` recursive, `*` single segment)
	// matched against the repo-relative path of every function in
	// the merged coverprofile. Longest-prefix wins.
	Packages []Layer `mapstructure:"packages"`

	// Excludes drops functions whose path matches any entry from
	// the threshold check. Excluded functions are counted in the
	// report under "excluded" but never fail the build.
	Excludes []Exclude `mapstructure:"excludes"`

	// Skips lists structural skip rules orthogonal to layer
	// thresholds: a function whose name matches FuncGlob AND whose
	// file matches FileGlob is counted under "skipped" but never
	// fails. Used for assertion branches the verifier framework
	// only exercises against broken implementations.
	Skips []Skip `mapstructure:"skips"`

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

// Exclude carries one path glob the coverage check ignores. The
// reason is human-facing only — it documents WHY the path is
// exempt so reviewers can challenge new excludes on PR.
type Exclude struct {
	Path   string `mapstructure:"path"`
	Reason string `mapstructure:"reason"`
}

// Skip declares a structural skip rule. A function matching BOTH
// FuncGlob and FileGlob is reported as skipped (under Label) but
// never fails the threshold check.
//
// Globs use shell-glob syntax: `*` matches any sequence except a
// path separator; literal characters match themselves. The label
// is human-facing and appears in the per-target summary.
type Skip struct {
	Label    string `mapstructure:"label"`
	FuncGlob string `mapstructure:"func_glob"`
	FileGlob string `mapstructure:"file_glob"`
}

// Defaults returns an empty Config. Coverage thresholds are an
// explicit project policy — there is no useful baseline ergon can
// supply that wouldn't be wrong for most repositories.
func Defaults() Config {
	return Config{
		TopN: 10,
	}
}
