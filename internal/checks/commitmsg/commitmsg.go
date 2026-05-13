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

// subjectPattern matches the `<type>(<scope>)?: <description>` shape
// Conventional Commits requires. The first capture is the type;
// the rest is content the package inspects for length and
// trailing-period rules.
var subjectPattern = regexp.MustCompile(`^(\w+)(\([^)]+\))?:\s+(.+)$`)

// ErrInvalidFormat reports that the subject does not match the
// Conventional Commits `<type>(<scope>?): <description>` shape.
var ErrInvalidFormat = errors.New("commitmsg: subject does not match Conventional Commits format")

// ErrUnknownType reports that the subject's type is not in the
// configured [Config.Types] set.
var ErrUnknownType = errors.New("commitmsg: unknown commit type")

// ErrSubjectTooLong reports that the subject exceeds the
// configured [Config.MaxSubjectLength].
var ErrSubjectTooLong = errors.New("commitmsg: subject exceeds maximum length")

// ErrTrailingPeriod reports that the subject ends with `.`, which
// the convention disallows.
var ErrTrailingPeriod = errors.New("commitmsg: subject must not end with a period")

// Run reads the commit message from path and validates its first
// line against cfg. Wraps [Validate] for the script-style entry
// point pre-commit hooks call.
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

// Validate checks that message's first line conforms to the
// Conventional Commits subset documented at the package level.
// Returns one of the package-level sentinels on failure.
func Validate(message string, cfg Config) error {
	cfg = withDefaults(cfg)
	subject, _, _ := strings.Cut(message, "\n")
	subject = strings.TrimRight(subject, "\r")

	match := subjectPattern.FindStringSubmatch(subject)
	if match == nil {
		return fmt.Errorf("%w: got %q", ErrInvalidFormat, subject)
	}
	typ := match[1]
	if !slices.Contains(cfg.Types, typ) {
		return fmt.Errorf("%w: %q (expected one of %s)",
			ErrUnknownType, typ, strings.Join(cfg.Types, ", "))
	}
	if len(subject) > cfg.MaxSubjectLength {
		return fmt.Errorf("%w: %d > %d", ErrSubjectTooLong, len(subject), cfg.MaxSubjectLength)
	}
	if strings.HasSuffix(subject, ".") {
		return fmt.Errorf("%w: %q", ErrTrailingPeriod, subject)
	}
	return nil
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
	return cfg
}
