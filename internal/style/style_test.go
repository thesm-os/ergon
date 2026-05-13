// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package style

import (
	"bytes"
	"strings"
	"testing"
)

// TestDetect pins the colour-suppression contract: a non-TTY
// writer (bytes.Buffer, file pipe, etc.) yields an empty Style so
// CI logs stay free of ANSI noise. Cannot use t.Parallel because
// one subtest mutates the NO_COLOR environment variable via
// t.Setenv.
func TestDetect(t *testing.T) {
	t.Run("non-TTY writer suppresses colour", func(t *testing.T) {
		var buf bytes.Buffer
		s := Detect(&buf)
		if s.Bold != "" || s.Red != "" || s.Reset != "" {
			t.Fatalf("Style on non-TTY = %+v, want empty", s)
		}
	})

	t.Run("NO_COLOR=1 suppresses colour even when supplied", func(t *testing.T) {
		// t.Setenv forbids t.Parallel on this subtest.
		t.Setenv("NO_COLOR", "1")
		var buf bytes.Buffer
		s := Detect(&buf)
		if s.Bold != "" {
			t.Fatalf("Style = %+v, want empty with NO_COLOR set", s)
		}
	})
}

// TestRule pins the horizontal-divider output.
func TestRule(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	(Style{}).Rule(&buf) // empty Style, no escapes
	got := buf.String()
	if !strings.Contains(got, "──") {
		t.Fatalf("Rule output = %q, want box-drawing dashes", got)
	}
}

// TestHeader pins the title-with-rules shape every check command
// uses to open a per-target section.
func TestHeader(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	(Style{}).Header(&buf, "foundation", "line ≥ 100%")
	got := buf.String()
	if !strings.Contains(got, "foundation") {
		t.Fatalf("Header missing title: %q", got)
	}
	if !strings.Contains(got, "line ≥ 100%") {
		t.Fatalf("Header missing details: %q", got)
	}
	// Two rule lines bracket the title.
	if strings.Count(got, "──") < 2 {
		t.Fatalf("Header should have two rule lines: %q", got)
	}
}

// TestVerdict pins the pass/fail badge.
func TestVerdict(t *testing.T) {
	t.Parallel()

	if !strings.Contains((Style{}).Verdict(true), "PASS") {
		t.Fatalf("Verdict(true) = %q, want PASS", (Style{}).Verdict(true))
	}
	if !strings.Contains((Style{}).Verdict(false), "FAIL") {
		t.Fatalf("Verdict(false) = %q, want FAIL", (Style{}).Verdict(false))
	}
}

// TestIndent pins the multi-line indentation helper used to nest
// subprocess output inside per-target reports.
func TestIndent(t *testing.T) {
	t.Parallel()

	got := Indent("line one\nline two\n\nline four", "  ")
	want := "  line one\n  line two\n\n  line four"
	if got != want {
		t.Fatalf("Indent = %q, want %q", got, want)
	}
}
