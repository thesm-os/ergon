// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"regexp"
	"strings"
)

// CommitInfo is the conventional-commit-relevant slice of a git
// commit: the subject line plus the body. The release binary uses
// only these two fields; the broader git.Commit-style shape would
// add author/date/SHA which inference doesn't consume.
type CommitInfo struct {
	// Subject is the commit's first line, no trailing newline.
	Subject string

	// Body is everything after the subject (and the blank
	// separator line). Empty when the commit has no body.
	Body string
}

// breakingSubjectRegex matches the `<type>!:` or `<type>(scope)!:`
// shape conventional-commits reserves for breaking changes. The
// leading `!` is what marks the commit as breaking.
var breakingSubjectRegex = regexp.MustCompile(`^[a-z]+(\([^)]+\))?!:`)

// featSubjectRegex matches the `feat:` and `feat(scope):` shapes,
// without the breaking `!` modifier.
var featSubjectRegex = regexp.MustCompile(`^feat(\([^)]+\))?:`)

// ClassifyCommits returns the highest [BumpLevel] any of the
// supplied commits warrants per the conventional-commit subset the
// binary supports. Returns [BumpNone] when commits is empty.
//
// The rule precedence (highest first) per commit:
//
//  1. `<type>!:` subject OR `BREAKING CHANGE` token anywhere in
//     the body → BumpMajor.
//  2. `feat:` / `feat(scope):` subject → BumpMinor.
//  3. Anything else → BumpPatch.
//
// The function takes the maximum across every commit so one
// breaking-change commit in a range of patch-only siblings still
// produces BumpMajor.
func ClassifyCommits(commits []CommitInfo) BumpLevel {
	level := BumpNone
	for _, c := range commits {
		next := classifyOne(c)
		if next > level {
			level = next
		}
	}
	return level
}

// classifyOne returns the per-commit [BumpLevel] per the rules
// enumerated on [ClassifyCommits].
func classifyOne(c CommitInfo) BumpLevel {
	if isBreaking(c) {
		return BumpMajor
	}
	if featSubjectRegex.MatchString(c.Subject) {
		return BumpMinor
	}
	return BumpPatch
}

// isBreaking reports whether the commit signals a breaking change
// per either of the conventional-commit conventions:
//
//   - The subject's `<type>(scope)!:` form (the `!` is the
//     declarative marker).
//   - A `BREAKING CHANGE:` (or `BREAKING-CHANGE:`) trailer
//     anywhere in the body.
func isBreaking(c CommitInfo) bool {
	if breakingSubjectRegex.MatchString(c.Subject) {
		return true
	}
	body := c.Body
	if body == "" {
		return false
	}
	return strings.Contains(body, "BREAKING CHANGE") ||
		strings.Contains(body, "BREAKING-CHANGE")
}
