// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"testing"
)

// TestParsePProfTop pins the canonical `go tool pprof -top`
// output against the parser: header metadata (Type, Total) flows
// into the summary, and each table row populates one [ProfileRow]
// with its native-unit Flat/Cum strings plus floating-point
// percent values.
func TestParsePProfTop(t *testing.T) {
	t.Parallel()

	t.Run("cpu profile with multiple rows", func(t *testing.T) {
		t.Parallel()
		out := `File: style.test
Type: cpu
Time: 2026-05-13 22:38:09 CEST
Duration: 2.50s, Total samples = 3560ms (142.22%)
Showing nodes accounting for 3460ms, 97.19% of 3560ms total
      flat  flat%   sum%        cum   cum%
    1170ms 32.87% 32.87%     1170ms 32.87%  runtime.kevent
     430ms 12.08% 44.94%      430ms 12.08%  runtime.pthread_cond_wait
      50ms  1.40% 88.48%      340ms  9.55%  go.thesmos.sh/ergon/internal/style.Indent
`
		summary := parsePProfTop("cpu", out)
		if summary.Type != "cpu" {
			t.Fatalf("Type = %q, want cpu", summary.Type)
		}
		if summary.Total != "3560ms" {
			t.Fatalf("Total = %q, want 3560ms", summary.Total)
		}
		if len(summary.Rows) != 3 {
			t.Fatalf("rows = %d, want 3", len(summary.Rows))
		}
		// First row: 1170ms 32.87% 32.87% 1170ms 32.87% runtime.kevent
		wantR0 := ProfileRow{
			Flat: "1170ms", FlatPct: 32.87,
			Cum: "1170ms", CumPct: 32.87,
			Symbol: "runtime.kevent",
		}
		if summary.Rows[0] != wantR0 {
			t.Errorf("rows[0] = %+v, want %+v", summary.Rows[0], wantR0)
		}
		// Third row: symbol with package path is preserved as one token.
		r2 := summary.Rows[2]
		if r2.Symbol != "go.thesmos.sh/ergon/internal/style.Indent" {
			t.Errorf("rows[2].Symbol = %q, want full import path", r2.Symbol)
		}
		if r2.FlatPct != 1.40 || r2.CumPct != 9.55 {
			t.Errorf("rows[2] percents = (%v, %v), want (1.40, 9.55)", r2.FlatPct, r2.CumPct)
		}
	})

	t.Run("mem profile (alloc_space) parses byte units verbatim", func(t *testing.T) {
		t.Parallel()
		out := `File: style.test
Type: alloc_space
Showing nodes accounting for 2.05kB, 100% of 2.05kB total
      flat  flat%   sum%        cum   cum%
   2.05kB   100% 100%     2.05kB   100%  go.thesmos.sh/ergon/internal/style.Indent
`
		summary := parsePProfTop("mem", out)
		if summary.Type != "alloc_space" {
			t.Fatalf("Type = %q, want alloc_space", summary.Type)
		}
		if len(summary.Rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(summary.Rows))
		}
		r := summary.Rows[0]
		if r.Flat != "2.05kB" || r.Cum != "2.05kB" {
			t.Errorf("byte units lost: row = %+v", r)
		}
		if r.FlatPct != 100 || r.CumPct != 100 {
			t.Errorf("percent parse failed: row = %+v", r)
		}
	})

	t.Run("symbol with method-receiver and inline marker is preserved", func(t *testing.T) {
		t.Parallel()
		out := `File: x.test
Type: cpu
Showing nodes accounting for 1ms, 100% of 1ms total
      flat  flat%   sum%        cum   cum%
       1ms   100%   100%        1ms   100%  strings.(*Builder).WriteString (inline)
`
		summary := parsePProfTop("cpu", out)
		if len(summary.Rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(summary.Rows))
		}
		want := "strings.(*Builder).WriteString (inline)"
		if got := summary.Rows[0].Symbol; got != want {
			t.Errorf("Symbol = %q, want %q", got, want)
		}
	})

	t.Run("metadata-only output yields zero rows", func(t *testing.T) {
		t.Parallel()
		out := "File: x.test\nType: cpu\nDuration: 0s, Total samples = 0\n"
		summary := parsePProfTop("cpu", out)
		if len(summary.Rows) != 0 {
			t.Fatalf("rows = %d, want 0", len(summary.Rows))
		}
	})
}

// TestParsePProfPercent pins the percent-suffix parser.
func TestParsePProfPercent(t *testing.T) {
	t.Parallel()

	t.Run("trailing %", func(t *testing.T) {
		t.Parallel()
		v, err := parsePProfPercent("32.87%")
		if err != nil || v != 32.87 {
			t.Fatalf("got (%v, %v), want (32.87, nil)", v, err)
		}
	})

	t.Run("missing % is rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := parsePProfPercent("32.87"); err == nil {
			t.Fatal("expected error for bare number")
		}
	})
}
