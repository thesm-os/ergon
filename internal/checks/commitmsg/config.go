// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package commitmsg validates a commit message against the
// project's Conventional Commits subset. Used by the pre-commit
// `commit-msg` stage so non-conforming commits never enter
// history; the Go-native validator avoids the Node dependency
// commitlint would pull in.
//
// The subset matches `.commitlintrc.yml`'s rule set:
//
//   - type-enum:           subject's <type> ∈ [Config.Types].
//   - header-max-length:   subject ≤ [Config.MaxSubjectLength].
//   - subject-empty:       <description> is non-empty.
//   - subject-full-stop:   <description> does not end with `.`.
//   - body-leading-blank:  line 2 is blank when a body is present.
//   - body-max-line-length: every body/footer line ≤
//     [Config.BodyMaxLineLength].
//
// Breaking-change marker `!` (`feat!:`, `feat(scope)!:`) is
// accepted as part of the subject shape.
package commitmsg

// Config carries the per-repository overrides for [Validate].
// Defaults match the standard Conventional Commits type set; repos
// with project-specific types extend [Config.Types].
type Config struct {
	// Types is the closed set of accepted commit types. The
	// subject must match `<type>(<scope>?)!?: <description>`
	// where <type> is one of these strings.
	Types []string `yaml:"types"`

	// Scopes optionally constrains the `(scope)` group to a closed
	// set. An empty slice (the default) leaves scopes free-form —
	// commits without a scope, or with any scope name, are all
	// accepted. A non-empty slice mirrors commitlint's
	// `scope-enum` rule: when present, the scope MUST be one of
	// these strings; commits without a scope still pass.
	Scopes []string `yaml:"scopes"`

	// MaxSubjectLength is the maximum length (in bytes) of the
	// commit subject line. Subjects longer than this are rejected;
	// the conventional limit is 72.
	MaxSubjectLength int `yaml:"max_subject_length"`

	// BodyMaxLineLength is the maximum length (in bytes) of any
	// individual line in the commit body or footer. The
	// conventional limit is 100. A zero value disables the check.
	BodyMaxLineLength int `yaml:"body_max_line_length"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the commit-msg section. The
// type set mirrors what `.commitlintrc.yml` enforces in the
// ecosystem repos.
func Defaults() Config {
	return Config{
		Types: []string{
			"feat", "fix", "docs", "refactor", "test",
			"ci", "chore", "perf", "build", "revert",
		},
		MaxSubjectLength:  72,
		BodyMaxLineLength: 100,
	}
}
