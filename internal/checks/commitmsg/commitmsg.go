// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package commitmsg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// subjectPattern matches the Conventional Commits subject shape:
//
//	<type>(<scope>)?!?: <description>
//
// The optional `(scope)` and breaking-change marker `!` are both
// accepted. The captures are: [1] type, [2] full `(scope)`
// including parens (may be empty), [3] `!` when present, [4]
// <description>.
var subjectPattern = regexp.MustCompile(`^(\w+)(\([^)]+\))?(!)?:\s+(.+)$`)

// ErrInvalidFormat reports that the subject does not match the
// Conventional Commits `<type>(<scope>?)!?: <description>` shape.
var ErrInvalidFormat = errors.New("commitmsg: subject does not match Conventional Commits format")

// ErrUnknownType reports that the subject's type is not in the
// configured [Config.Types] set.
var ErrUnknownType = errors.New("commitmsg: unknown commit type")

// ErrUnknownScope reports that the subject's `(scope)` is not in
// the configured [Config.Scopes] set. Only emitted when Scopes is
// non-empty; the default (empty) leaves scopes free-form.
var ErrUnknownScope = errors.New("commitmsg: unknown commit scope")

// ErrSubjectTooLong reports that the subject exceeds the
// configured [Config.MaxSubjectLength].
var ErrSubjectTooLong = errors.New("commitmsg: subject exceeds maximum length")

// ErrTrailingPeriod reports that the subject ends with `.`, which
// the convention disallows.
var ErrTrailingPeriod = errors.New("commitmsg: subject must not end with a period")

// ErrBodyLeadingBlankMissing reports that the message has a body
// (content on line 3 or later) but no blank line separating the
// subject from it. Conventional Commits requires the blank.
var ErrBodyLeadingBlankMissing = errors.New("commitmsg: line 2 must be blank to separate the subject from the body")

// ErrBodyLineTooLong reports that one or more lines in the body
// or footer exceed [Config.BodyMaxLineLength].
var ErrBodyLineTooLong = errors.New("commitmsg: body line exceeds maximum length")

// Run reads the commit message from path and validates it
// against cfg. Wraps [Validate] for the script-style entry point
// pre-commit hooks call.
func Run(stdout, stderr io.Writer, path string, cfg Config) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := Validate(string(body), cfg); err != nil {
		fmt.Fprintf(stderr, "commit-msg: %v\n", err)
		return err
	}
	fmt.Fprintln(stdout, "commit-msg: OK")
	return nil
}

// Validate checks that message conforms to the Conventional
// Commits subset documented at the package level. Returns one of
// the package-level sentinels on failure.
func Validate(message string, cfg Config) error {
	cfg = withDefaults(cfg)
	message = stripCommentLines(message)
	lines := splitLines(message)
	if len(lines) == 0 {
		return fmt.Errorf("%w: empty message", ErrInvalidFormat)
	}

	subject := strings.TrimRight(lines[0], "\r")
	if err := validateSubject(subject, cfg); err != nil {
		return err
	}
	return validateBody(lines, cfg)
}

// validateSubject pins the rules that apply to line 1: the
// `<type>(<scope>)?!?: <description>` shape, the configured type
// set, the optional scope-enum, the maximum length, and the
// no-trailing-period rule.
func validateSubject(subject string, cfg Config) error {
	match := subjectPattern.FindStringSubmatch(subject)
	if match == nil {
		return fmt.Errorf("%w: got %q", ErrInvalidFormat, subject)
	}
	typ := match[1]
	if !slices.Contains(cfg.Types, typ) {
		return fmt.Errorf("%w: %q (expected one of %s)",
			ErrUnknownType, typ, strings.Join(cfg.Types, ", "))
	}
	// Scope appears as match[2] including the surrounding parens
	// ("(api)"); strip them before comparing against the allow-list.
	// When Scopes is empty the rule is disabled — every scope (and
	// the absence of one) is accepted.
	if len(cfg.Scopes) > 0 && match[2] != "" {
		scope := strings.TrimSuffix(strings.TrimPrefix(match[2], "("), ")")
		if !slices.Contains(cfg.Scopes, scope) {
			return fmt.Errorf("%w: %q (expected one of %s)",
				ErrUnknownScope, scope, strings.Join(cfg.Scopes, ", "))
		}
	}
	if len(subject) > cfg.MaxSubjectLength {
		return fmt.Errorf("%w: %d > %d", ErrSubjectTooLong, len(subject), cfg.MaxSubjectLength)
	}
	if strings.HasSuffix(subject, ".") {
		return fmt.Errorf("%w: %q", ErrTrailingPeriod, subject)
	}
	return nil
}

// validateBody pins the rules that apply to lines 2..N:
//
//   - Line 2 MUST be blank (whitespace-only) — the
//     subject-from-body separator
//     (`commitlint.body-leading-blank`).
//   - Every line in the body or footer must be at most
//     [Config.BodyMaxLineLength] bytes
//     (`commitlint.body-max-line-length`). The check is skipped
//     when BodyMaxLineLength is zero.
//
// A message with only a subject line satisfies both rules; the
// implicit "no body" case never trips them.
func validateBody(lines []string, cfg Config) error {
	if len(lines) < 2 {
		return nil
	}
	line2 := strings.TrimRight(lines[1], "\r")
	if strings.TrimSpace(line2) != "" {
		return fmt.Errorf("%w: got %q", ErrBodyLeadingBlankMissing, line2)
	}
	if cfg.BodyMaxLineLength == 0 {
		return nil
	}
	for i, line := range lines[1:] {
		line = strings.TrimRight(line, "\r")
		if len(line) > cfg.BodyMaxLineLength {
			return fmt.Errorf("%w: line %d is %d > %d bytes",
				ErrBodyLineTooLong, i+2, len(line), cfg.BodyMaxLineLength)
		}
	}
	return nil
}

// stripCommentLines drops every line that starts with `#` — the
// convention git uses to mark comments in `COMMIT_EDITMSG` that
// must not enter the recorded message. Without this filter every
// commit message that goes through `git commit` (without -m)
// would fail body-line-length on the git status block.
func stripCommentLines(message string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(message, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// splitLines splits message on `\n` and drops a single trailing
// empty line (the EOF-trailing-newline most editors add). Inner
// blank lines are preserved so the body-leading-blank check can
// inspect line 2.
func splitLines(message string) []string {
	if message == "" {
		return nil
	}
	lines := strings.Split(message, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// withDefaults fills any zero-value field on cfg from [Defaults].
func withDefaults(cfg Config) Config {
	d := Defaults()
	if len(cfg.Types) == 0 {
		cfg.Types = d.Types
	}
	if cfg.MaxSubjectLength == 0 {
		cfg.MaxSubjectLength = d.MaxSubjectLength
	}
	if cfg.BodyMaxLineLength == 0 {
		cfg.BodyMaxLineLength = d.BodyMaxLineLength
	}
	return cfg
}
