// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package style produces the colored, ruled, bold-titled output
// the project's check commands emit. Detection of a TTY is
// automatic — when stdout is a file or pipe the colour codes are
// suppressed so CI logs stay clean.
//
// Style is a value type carrying the ANSI escapes; callers
// construct it once with [Detect] and pass it through.
package style

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Style carries the ANSI escape sequences for one output stream.
// A zero value emits no colour, which is the right behaviour when
// stdout is not a terminal.
type Style struct {
	Bold   string
	Dim    string
	Green  string
	Red    string
	Yellow string
	Blue   string
	Reset  string
}

// Detect returns a Style with ANSI escapes when w is a terminal
// and the NO_COLOR environment variable is unset. Honours the
// de-facto `NO_COLOR=1` opt-out for accessibility.
func Detect(w io.Writer) Style {
	if os.Getenv("NO_COLOR") != "" {
		return Style{}
	}
	if !isTTY(w) {
		return Style{}
	}
	return Style{
		Bold:   "\033[1m",
		Dim:    "\033[2m",
		Green:  "\033[32m",
		Red:    "\033[31m",
		Yellow: "\033[33m",
		Blue:   "\033[36m",
		Reset:  "\033[0m",
	}
}

// isTTY reports whether w refers to a character device — the
// canonical test for "is this stdout connected to a terminal".
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// rule is the horizontal divider every per-target section opens
// and closes with. 70 box-drawing dashes matches the bash scripts.
const rule = "──────────────────────────────────────────────────────────────────────"

// Rule writes the dim horizontal divider to w.
func (s Style) Rule(w io.Writer) {
	fmt.Fprintf(w, "%s%s%s\n", s.Dim, rule, s.Reset)
}

// Header writes a section header: a dim rule, the title in bold,
// the details column unformatted, then a closing dim rule.
//
// The shape matches the bash scripts so users moving between
// `make check` (old) and `ergon check` (new) see the same layout.
func (s Style) Header(w io.Writer, title, details string) {
	s.Rule(w)
	fmt.Fprintf(w, "  %s%s%s    %s\n", s.Bold, title, s.Reset, details)
	s.Rule(w)
}

// Verdict returns the bold-coloured "✓ PASS" or "✗ FAIL" badge
// other lines compose into their output.
func (s Style) Verdict(pass bool) string {
	if pass {
		return s.Bold + s.Green + "✓ PASS" + s.Reset
	}
	return s.Bold + s.Red + "✗ FAIL" + s.Reset
}

// Pass returns the bold-green "PASS" badge with the leading mark,
// for inline use ("Score:    95%   ✓ PASS").
func (s Style) Pass() string {
	return s.Green + "✓ PASS" + s.Reset
}

// Fail returns the bold-red "FAIL" badge with the leading mark.
func (s Style) Fail() string {
	return s.Red + "✗ FAIL" + s.Reset
}

// Warn returns the yellow "WARN" badge.
func (s Style) Warn() string {
	return s.Yellow + "WARN" + s.Reset
}

// Bolded wraps s in bold escapes.
func (s Style) Bolded(text string) string {
	return s.Bold + text + s.Reset
}

// Dimmed wraps s in dim escapes.
func (s Style) Dimmed(text string) string {
	return s.Dim + text + s.Reset
}

// FinalVerdict writes the closing per-run verdict block: a rule,
// the verdict line, another rule. Use after every per-target
// section has rendered.
func (s Style) FinalVerdict(w io.Writer, pass bool, message string) {
	s.Rule(w)
	if pass {
		fmt.Fprintf(w, "  %s%s✓ PASS%s    %s\n", s.Bold, s.Green, s.Reset, message)
	} else {
		fmt.Fprintf(w, "  %s%s✗ FAIL%s    %s\n", s.Bold, s.Red, s.Reset, message)
	}
	s.Rule(w)
}

// Indent re-prefixes every non-empty line of body with the given
// indent. Used to nest captured subprocess output (e.g. benchstat)
// into the per-target report.
func Indent(body, indent string) string {
	var out strings.Builder
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" {
			out.WriteString("\n")
			continue
		}
		out.WriteString(indent)
		out.WriteString(line)
		out.WriteString("\n")
	}
	// Trim the trailing newline we added; the caller controls
	// final spacing.
	return strings.TrimSuffix(out.String(), "\n")
}
