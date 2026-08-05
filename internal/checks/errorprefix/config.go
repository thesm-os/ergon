// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package errorprefix enforces the convention that every
// `errors.New("...")` sentinel starts with the file's package
// name (or a `<pkg>.<sub>:` qualifier for sub-packages). The
// rule keeps sentinel messages self-identifying when surfaced
// far from the point of definition.
package errorprefix

import "go.thesmos.sh/ergon/internal/checks/policy"

// Config carries the per-repository overrides for [Run]. The
// default scan root is the working tree below the repository
// root; repos that want to narrow scope (e.g. only the library
// layers, not cmd/) override [Config.TargetDirs].
type Config struct {
	// TargetDirs is the list of directories (relative to the
	// repository root) to scan. The walker recurses into each.
	// Defaults to `[.]` (the entire repository).
	TargetDirs []string `yaml:"target_dirs"`

	// Excludes exempts paths the rule must not apply to, using the
	// same glob syntax and {path, reason} shape as
	// `checks.excludes` so the two read alike. Empty by default:
	// nothing is exempt unless a repository says so.
	//
	// TargetDirs cannot express an exemption. Its entries are
	// inclusive prefixes, so a repository with root-level .go
	// files can only reach them with `.` — which reaches
	// everything else too. That is the shape a generator's fixture
	// corpus hits: source that exists to be read by a generator,
	// deliberately holding the patterns the generator rejects.
	// Without an exemption the lint forces such a fixture to be
	// prefixed, and a prefixed fixture can no longer detect a
	// generator that ignored its suppression directive.
	//
	// The list is kept separate from `checks.excludes` on purpose.
	// That one is shared by coverage and mutation because a file
	// exempt from one gate is exempt from the other; a naming
	// convention is a different question, and a corpus exempt from
	// this rule is not thereby exempt from coverage.
	//
	// NOTE the glob syntax differs from TargetDirs above: entries
	// here are anchored globs, so `conformance/corpus` matches that
	// path alone and `conformance/corpus/...` is needed to match
	// the tree beneath it.
	Excludes []policy.Exclude `yaml:"excludes"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the error-prefix section.
func Defaults() Config {
	return Config{
		TargetDirs: []string{"."},
	}
}

// excluded reports whether rel is exempt under cfg.
//
// Callers apply it only to files already in scope: out-of-scope and
// exempt are different states, and conflating them inflates the
// count that exists to keep exemptions reviewable.
func excluded(rel string, cfg Config) bool {
	return policy.MatchesExclude(rel, cfg.Excludes)
}
