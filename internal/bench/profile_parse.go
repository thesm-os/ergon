// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"regexp"
	"strconv"
	"strings"
)

// Env records the `goos: / goarch: / pkg: / cpu:` preamble
// every `go test -bench` run prints before its benchmark rows.
// All four fields are optional — a benchmark run that produces
// no env block leaves the corresponding strings empty.
type Env struct {
	GOOS, GOARCH, Pkg, CPU string
}

// Row records one `BenchmarkName-N  iters  ns/op  ...` line.
// B/op and Allocs are optional in the upstream format; missing
// columns are zero in the parsed row.
type Row struct {
	// Name is the canonical `BenchmarkXxx-N` token, GOMAXPROCS
	// suffix included.
	Name string

	// N is the iteration count `go test -bench` ran the body for.
	N int64

	// NsPerOp is the per-operation wall time in nanoseconds.
	NsPerOp float64

	// BPerOp is bytes allocated per operation (zero when the
	// run did not include `-benchmem`).
	BPerOp int64

	// Allocs is the allocation count per operation (zero without
	// `-benchmem`).
	Allocs int64
}

// benchRowPattern matches one canonical bench output line:
//
//	BenchmarkName-14    iters    ns/op   [bytes B/op]   [allocs allocs/op]
//
// The B/op and allocs/op columns are optional (only present
// under `-benchmem`); the regex captures them in groups 4 and 5
// so the consumer can leave the corresponding row fields zero
// when absent.
var benchRowPattern = regexp.MustCompile(
	`^(Benchmark\S+)\s+(\d+)\s+([0-9.]+)\s+ns/op` +
		`(?:\s+(\d+)\s+B/op)?` +
		`(?:\s+(\d+)\s+allocs/op)?`,
)

// parseBenchOutput walks the captured `go test -bench` output of
// a single package and returns the environment block plus every
// recognised benchmark row. Unrecognised lines (PASS / ok /
// status / blanks) are skipped without error so a malformed
// trailer never tanks the parse.
func parseBenchOutput(out string) (Env, []Row) {
	var env Env
	var rows []Row
	for line := range strings.SplitSeq(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "goos:"):
			env.GOOS = strings.TrimSpace(strings.TrimPrefix(trimmed, "goos:"))
		case strings.HasPrefix(trimmed, "goarch:"):
			env.GOARCH = strings.TrimSpace(strings.TrimPrefix(trimmed, "goarch:"))
		case strings.HasPrefix(trimmed, "pkg:"):
			env.Pkg = strings.TrimSpace(strings.TrimPrefix(trimmed, "pkg:"))
		case strings.HasPrefix(trimmed, "cpu:"):
			env.CPU = strings.TrimSpace(strings.TrimPrefix(trimmed, "cpu:"))
		default:
			if row, ok := parseRow(trimmed); ok {
				rows = append(rows, row)
			}
		}
	}
	return env, rows
}

// parseRow extracts a single benchmark row from the line.
// The boolean second return is false when the line is not a
// recognised benchmark output (so the consumer can skip).
func parseRow(line string) (Row, bool) {
	m := benchRowPattern.FindStringSubmatch(line)
	if m == nil {
		return Row{}, false
	}
	row := Row{Name: m[1]}
	row.N, _ = strconv.ParseInt(m[2], 10, 64)
	row.NsPerOp, _ = strconv.ParseFloat(m[3], 64)
	if m[4] != "" {
		row.BPerOp, _ = strconv.ParseInt(m[4], 10, 64)
	}
	if m[5] != "" {
		row.Allocs, _ = strconv.ParseInt(m[5], 10, 64)
	}
	return row, true
}
