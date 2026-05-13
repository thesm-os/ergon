// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package style

import (
	"bytes"
	"errors"
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

// TestHeader pins the title-with-rule shape every gate section
// uses to open. There is exactly one rule (above the title);
// adjacent sections share the next section's opening rule, so
// stacking two would drown the report in horizontal chrome.
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
	// Exactly one rule line — adjacent sections share the next
	// section's opening rule as their separator.
	if n := strings.Count(got, rule); n != 1 {
		t.Fatalf("Header should have exactly one rule line, got %d: %q", n, got)
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

// TestSummary pins the stage-closing block: per-target verdict
// lines, the aggregate verdict, label padding, and the boolean
// return value reflecting whether any target failed.
func TestSummary(t *testing.T) {
	t.Parallel()

	t.Run("every-pass run returns false and prints the pass message", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		failed := (Style{}).Summary(&buf, []StageResult{
			{Label: "a"},
			{Label: "b"},
		}, "every module passed", "")
		if failed {
			t.Fatal("Summary returned failed=true for all-passing input")
		}
		body := buf.String()
		if !strings.Contains(body, "PASS") {
			t.Fatalf("Summary missing PASS marks: %q", body)
		}
		if !strings.Contains(body, "every module passed") {
			t.Fatalf("Summary missing pass message: %q", body)
		}
	})

	t.Run("any failure returns true and surfaces the failMessage", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		failed := (Style{}).Summary(&buf, []StageResult{
			{Label: "a"},
			{Label: "b", Err: errors.New("boom"), Note: "boom"},
		}, "every module passed", "1 of 2 failed")
		if !failed {
			t.Fatal("Summary returned failed=false when one target carried Err")
		}
		body := buf.String()
		if !strings.Contains(body, "1 of 2 failed") {
			t.Fatalf("Summary missing failMessage: %q", body)
		}
		if !strings.Contains(body, "FAIL") {
			t.Fatalf("Summary missing FAIL mark: %q", body)
		}
	})

	t.Run("skipped target renders a dimmed dash, not a verdict", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		(Style{}).Summary(&buf, []StageResult{
			{Label: "tests", Skipped: true, Note: "no packages"},
		}, "every module passed", "")
		// Look at the per-target line for [tests] specifically;
		// the aggregate footer below it legitimately says PASS.
		body := buf.String()
		skipLine := ""
		for line := range strings.SplitSeq(body, "\n") {
			if strings.Contains(line, "[tests]") {
				skipLine = line
				break
			}
		}
		if skipLine == "" {
			t.Fatalf("no [tests] line in output: %q", body)
		}
		if strings.Contains(skipLine, "PASS") || strings.Contains(skipLine, "FAIL") {
			t.Fatalf("skip line carries a verdict mark: %q", skipLine)
		}
		if !strings.Contains(skipLine, "no packages") {
			t.Fatalf("skip line missing note: %q", skipLine)
		}
	})

	t.Run("labels are padded to a uniform width", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		(Style{}).Summary(&buf, []StageResult{
			{Label: "a"},
			{Label: "much-longer"},
		}, "ok", "")
		// Look for the short label padded out to the long one's width.
		if !strings.Contains(buf.String(), "[a          ]") {
			t.Fatalf("Summary label padding missing: %q", buf.String())
		}
	})

	t.Run("empty failMessage falls back to a default count", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		(Style{}).Summary(&buf, []StageResult{
			{Label: "a", Err: errors.New("nope")},
		}, "ok", "")
		if !strings.Contains(buf.String(), "1 of 1 target(s) failed") {
			t.Fatalf("Summary default fail message missing: %q", buf.String())
		}
	})
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

// BenchmarkIndent measures the cost of [Indent] over a realistic
// captured-tool-output body (~50 lines). The function is on the
// hot path of failed-stage rendering — every gate that surfaces
// captured output runs it once per failing per-target line — so
// regressions show up as a higher failed-stage wall time.
func BenchmarkIndent(b *testing.B) {
	body := strings.Repeat("file.go:42: some finding goes here\n", 50)
	b.ReportAllocs()
	for b.Loop() {
		_ = Indent(body, "      ")
	}
}
