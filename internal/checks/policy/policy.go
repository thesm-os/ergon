// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package policy declares the path-exclusion and structural-skip
// rules `ergon check coverage` and `ergon check mutation` share.
//
// Coverage and mutation enforce different thresholds against the
// same source tree, and a file or function that is exempt from
// one gate is invariably exempt from the other: generated string-
// ers carry unkillable mutants and are also not meaningful for
// coverage; conformance-suite definitions (`Run*Suite`) are the
// verification framework rather than the system under test, so
// they belong in neither gate. The two subsystems read the same
// [Exclude] and [Skip] lists so the project declares the rule
// once.
//
// Each subsystem applies the rules with the precision its
// underlying tooling allows: coverage filters per-function in its
// own parser; mutation translates the rules into the file-glob
// regex `gremlins --exclude-files` accepts (the [Skip] file-glob
// is honoured at file granularity since gremlins has no per-
// function exclusion).
package policy

import (
	"regexp"
	"strings"
)

// Exclude carries one path glob the gates ignore. The reason is
// human-facing only — it documents WHY the path is exempt so
// reviewers can challenge new entries on PR.
//
// Path uses the schema's glob syntax: `...` matches any sequence
// of path segments, `*` matches a single segment. See [GlobRegex]
// for the translation.
type Exclude struct {
	// Path is the glob the rule applies to.
	Path string `yaml:"path"`

	// Reason documents why the path is exempt. Surfaces nowhere
	// today but is required so the YAML reads as self-explanatory.
	Reason string `yaml:"reason"`
}

// Skip declares a structural-skip rule: a function whose name
// matches FuncGlob AND whose file matches FileGlob is exempt from
// every gate. Used for assertion / contract branches the verifier
// framework only exercises against broken implementations, and
// for conformance-suite entry points whose body is "iterate every
// model and assert."
//
// Globs use shell-glob syntax: `*` matches any sequence of
// characters (including path separators); literal characters
// match themselves. The Label is human-facing and surfaces in
// per-target reports.
//
// Coverage applies the (FuncGlob, FileGlob) pair function-by-
// function. Mutation, lacking a per-function exclusion knob in
// gremlins, applies the FileGlob alone — every mutant under a
// matching file is excluded.
type Skip struct {
	// Label names the rule for the per-target report.
	Label string `yaml:"label"`

	// FuncGlob is matched against the function's bare name.
	FuncGlob string `yaml:"func_glob"`

	// FileGlob is matched against the function's source file path.
	FileGlob string `yaml:"file_glob"`
}

// MatchesExclude reports whether path matches any exclude's Path
// glob. Uses the schema's `*` / `...` glob syntax via [GlobRegex].
func MatchesExclude(path string, excludes []Exclude) bool {
	for _, e := range excludes {
		re, err := regexp.Compile(GlobRegex(e.Path))
		if err != nil {
			continue
		}
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// MatchesSkip reports whether (funcName, path) satisfies any skip
// rule — both FuncGlob and FileGlob must match. Uses shell-style
// `*` globbing via [ShellGlobRegex].
func MatchesSkip(funcName, path string, skips []Skip) bool {
	for _, s := range skips {
		if !shellGlobMatch(s.FuncGlob, funcName) {
			continue
		}
		if !shellGlobMatch(s.FileGlob, path) {
			continue
		}
		return true
	}
	return false
}

// GremlinsExcludeRegex builds the single `--exclude-files` regex
// gremlins accepts from the path globs in excludes plus the
// FileGlob field of every skip. Returns an empty string when both
// inputs are empty so the caller can skip the flag.
//
// The translation `|`-joins one regex fragment per rule, each
// anchored at the end so glob-style suffix matching (`*test/spec.go`)
// behaves the way the bash predecessor's policy did.
func GremlinsExcludeRegex(excludes []Exclude, skips []Skip) string {
	var parts []string
	for _, e := range excludes {
		if e.Path == "" {
			continue
		}
		parts = append(parts, gremlinsPattern(e.Path))
	}
	for _, s := range skips {
		if s.FileGlob == "" {
			continue
		}
		parts = append(parts, gremlinsPattern(s.FileGlob))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, "|") + ")$"
}

// triplePlaceholder stands in for the `...` recursive wildcard
// during glob translation so the later `*` substitution does not
// re-process the `*` introduced by the triple expansion. NUL is
// safe because it cannot appear in a glob.
const triplePlaceholder = "\x00TRIPLE\x00"

// GlobRegex translates the schema's path-glob syntax (`*` matches
// within a segment, `...` matches any number of segments) into an
// anchored regex. Exposed so coverage can build the same patterns
// for its own classifier.
func GlobRegex(glob string) string {
	s := strings.ReplaceAll(glob, "...", triplePlaceholder)
	s = strings.ReplaceAll(s, ".", `\.`)
	s = strings.ReplaceAll(s, "*", ".*")
	s = strings.ReplaceAll(s, triplePlaceholder, ".*")
	return "^" + s + "$"
}

// gremlinsPattern translates one glob into the regex fragment
// gremlins' `--exclude-files` consumes. Unlike [GlobRegex] the
// fragment is unanchored at the start (gremlins matches against
// absolute paths) and does not carry a trailing `$` (the caller
// appends one to the joined group).
func gremlinsPattern(glob string) string {
	s := strings.ReplaceAll(glob, "...", triplePlaceholder)
	s = strings.ReplaceAll(s, ".", `\.`)
	s = strings.ReplaceAll(s, "*", ".*")
	return strings.ReplaceAll(s, triplePlaceholder, ".*")
}

// shellGlobMatch implements the shell-style glob used by structural
// skips: `*` matches any run of characters (including path
// separators), other characters match literally.
func shellGlobMatch(pattern, s string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	re := "^" + regexp.QuoteMeta(pattern) + "$"
	re = strings.ReplaceAll(re, `\*`, ".*")
	matched, err := regexp.MatchString(re, s)
	return err == nil && matched
}
