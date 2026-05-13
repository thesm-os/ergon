// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package commitmsg validates a commit subject line against the
// project's Conventional Commits subset. Used by the pre-commit
// `commit-msg` stage so non-conforming commits never enter
// history; the Go-native validator avoids the Node dependency
// commitlint would pull in.
package commitmsg

// Config carries the per-repository overrides for [Validate].
// Defaults match the standard Conventional Commits type set; repos
// with project-specific types extend [Config.Types].
type Config struct {
	// Types is the closed set of accepted commit types. The
	// subject must match `<type>(<scope>?): <description>` where
	// <type> is one of these strings.
	Types []string `mapstructure:"types"`

	// MaxSubjectLength is the maximum length (in bytes) of the
	// commit subject line. Subjects longer than this are rejected;
	// the conventional limit is 72.
	MaxSubjectLength int `mapstructure:"max_subject_length"`
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
		MaxSubjectLength: 72,
	}
}
