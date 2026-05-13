// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"testing"
)

// TestParseBenchOutput pins the canonical `go test -bench`
// output against the parser: env preamble fields populate, each
// `BenchmarkXxx-N` row produces one [BenchRow], optional B/op
// and allocs/op columns are zero when absent, and trailer lines
// (PASS / ok / failure) are skipped.
func TestParseBenchOutput(t *testing.T) {
	t.Parallel()

	t.Run("typical run populates env + every row", func(t *testing.T) {
		t.Parallel()
		out := `goos: darwin
goarch: arm64
pkg: go.example.com/x/pkg
cpu: Apple M4 Pro
BenchmarkA-14    1830078    1312 ns/op    7064 B/op    9 allocs/op
BenchmarkB-14      54321     412 ns/op
PASS
ok  go.example.com/x/pkg    2.761s
`
		env, rows := parseBenchOutput(out)
		wantEnv := Env{GOOS: "darwin", GOARCH: "arm64", Pkg: "go.example.com/x/pkg", CPU: "Apple M4 Pro"}
		if env != wantEnv {
			t.Fatalf("env = %+v, want %+v", env, wantEnv)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(rows))
		}
		wantA := Row{Name: "BenchmarkA-14", N: 1830078, NsPerOp: 1312, BPerOp: 7064, Allocs: 9}
		if rows[0] != wantA {
			t.Errorf("rows[0] = %+v, want %+v", rows[0], wantA)
		}
		// Row B omits B/op + allocs/op; both should be zero.
		wantB := Row{Name: "BenchmarkB-14", N: 54321, NsPerOp: 412}
		if rows[1] != wantB {
			t.Errorf("rows[1] = %+v, want %+v", rows[1], wantB)
		}
	})

	t.Run("empty output yields zero env + no rows", func(t *testing.T) {
		t.Parallel()
		env, rows := parseBenchOutput("")
		if env != (Env{}) {
			t.Fatalf("env = %+v, want zero", env)
		}
		if len(rows) != 0 {
			t.Fatalf("rows = %d, want 0", len(rows))
		}
	})

	t.Run("trailer-only output yields env-without-rows", func(t *testing.T) {
		t.Parallel()
		out := "goos: linux\nPASS\nok pkg 0.001s\n"
		env, rows := parseBenchOutput(out)
		if env.GOOS != "linux" {
			t.Fatalf("env.GOOS = %q, want linux", env.GOOS)
		}
		if len(rows) != 0 {
			t.Fatalf("rows = %d, want 0", len(rows))
		}
	})
}

// TestPackageSlug pins the import-path → slug rule the per-
// package output directories use.
func TestPackageSlug(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"go.thesmos.sh/ergon/internal/style", "go.thesmos.sh_ergon_internal_style"},
		{"pkg", "pkg"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := packageSlug(tc.in); got != tc.want {
			t.Errorf("packageSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHumanIters pins the thousands-grouped iteration formatter.
func TestHumanIters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{1234, "1,234"},
		{1830078, "1,830,078"},
		{1000000000, "1,000,000,000"},
	}
	for _, tc := range cases {
		if got := humanIters(tc.in); got != tc.want {
			t.Errorf("humanIters(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
