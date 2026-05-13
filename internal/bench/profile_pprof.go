// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// ProfileSummary records the parsed top-N output of one pprof
// artefact. Each artefact (cpu / mem / block / mutex) produces
// one summary; the renderer prints the rows in a fixed-width
// table beneath the artefact path.
type ProfileSummary struct {
	// Kind is the human-facing artefact label (cpu / mem / block
	// / mutex). Mirrors [profileArtefact.Kind] for the renderer.
	Kind string

	// Type is the pprof sample type the file carries ("cpu",
	// "alloc_space", "alloc_objects", "delay", "contentions"…).
	// Captured for the header line; not interpreted further.
	Type string

	// Total is the aggregate sample value as pprof's own summary
	// reports it, with its native unit ("3560ms", "2.05kB"). The
	// renderer surfaces it next to the artefact kind so the
	// reader knows the scale of the rows below.
	Total string

	// Rows is the top-N rows from `go tool pprof -top`. The
	// number of rows is bounded by the caller's TopN.
	Rows []ProfileRow
}

// ProfileRow is one line of pprof's `-top` table:
//
//	<flat> <flat%> <sum%> <cum> <cum%> <symbol>
//
// Flat / Cum keep their native unit suffix (`ms`, `kB`, …); the
// percent columns are parsed into [0, 100] floats so the
// renderer can format with consistent precision.
type ProfileRow struct {
	// Flat is the bare-string flat value (e.g. "1170ms",
	// "2.05kB"). Kept verbatim because pprof's auto-unit logic
	// picks the most-readable suffix and the renderer just
	// echoes it.
	Flat string

	// FlatPct is the same row's flat-percent column.
	FlatPct float64

	// Cum is the cumulative value, same shape as Flat.
	Cum string

	// CumPct is the same row's cum-percent column.
	CumPct float64

	// Symbol is the function name pprof printed last on the row.
	// Generic instantiations and method receivers are preserved
	// verbatim.
	Symbol string
}

// pprofHeaderTotalPattern matches the `Duration: ..., Total
// samples = <amount>` line every pprof -top output emits. Capture
// 1 is the total expression (`3560ms`, `2.05kB`, etc.). The line
// shape is stable across pprof versions back to Go 1.12.
var pprofHeaderTotalPattern = regexp.MustCompile(
	`Total samples = ([^\s(]+)`,
)

// pprofHeaderTypePattern matches the `Type: <kind>` line pprof
// prints near the top of `-top` output.
var pprofHeaderTypePattern = regexp.MustCompile(`(?m)^Type:\s+(\S+)`)

// pprofTableColumnHeader is the literal column-header row that
// separates pprof's metadata block from its body rows. Used as a
// marker by [parsePProfTop] to seek to the data block.
const pprofTableColumnHeader = "flat  flat%   sum%"

// summarizeProfile shells out to `go tool pprof -top
// -nodecount=N` and parses the result. Returns a populated
// [ProfileSummary] for the renderer.
//
// dir is the working directory the subprocess runs in. The
// caller is responsible for ensuring the profile file exists;
// `go tool pprof` surfaces a clear error when the path is wrong
// or the file is empty.
func summarizeProfile(
	ctx context.Context, runner xexec.Runner,
	dir, kind, path string, topN int,
) (ProfileSummary, error) {
	var buf bytes.Buffer
	args := []string{
		"tool", "pprof", "-top",
		"-nodecount=" + strconv.Itoa(topN), path,
	}
	if err := runner.Run(ctx,
		xexec.Options{Dir: dir, Stdout: &buf, Stderr: &buf},
		"go", args...); err != nil {
		return ProfileSummary{Kind: kind}, fmt.Errorf(
			"go tool pprof %s: %w: %s", kind, err,
			strings.TrimSpace(buf.String()),
		)
	}
	return parsePProfTop(kind, buf.String()), nil
}

// parsePProfTop parses the captured output of `go tool pprof
// -top` into a [ProfileSummary]. Header metadata (Type, Total)
// and the per-row table are both extracted; anything else is
// ignored.
func parsePProfTop(kind, out string) ProfileSummary {
	summary := ProfileSummary{Kind: kind}
	if m := pprofHeaderTypePattern.FindStringSubmatch(out); len(m) == 2 {
		summary.Type = m[1]
	}
	if m := pprofHeaderTotalPattern.FindStringSubmatch(out); len(m) == 2 {
		summary.Total = m[1]
	}
	summary.Rows = parsePProfRows(out)
	return summary
}

// parsePProfRows skips past pprof's metadata block (everything
// up to the `flat  flat%   sum%` column-header line) and parses
// each subsequent line as a [ProfileRow].
func parsePProfRows(out string) []ProfileRow {
	var rows []ProfileRow
	inTable := false
	for line := range strings.SplitSeq(out, "\n") {
		if !inTable {
			if strings.Contains(line, pprofTableColumnHeader) {
				inTable = true
			}
			continue
		}
		row, ok := parsePProfRow(line)
		if !ok {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

// parsePProfRow extracts one [ProfileRow] from a single body
// line. Returns ok=false when the line does not match the six-
// column shape so the caller can skip trailing blank or
// commentary lines.
func parsePProfRow(line string) (ProfileRow, bool) {
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return ProfileRow{}, false
	}
	flatPct, err := parsePProfPercent(fields[1])
	if err != nil {
		return ProfileRow{}, false
	}
	cumPct, err := parsePProfPercent(fields[4])
	if err != nil {
		return ProfileRow{}, false
	}
	return ProfileRow{
		Flat:    fields[0],
		FlatPct: flatPct,
		Cum:     fields[3],
		CumPct:  cumPct,
		Symbol:  strings.Join(fields[5:], " "),
	}, true
}

// parsePProfPercent turns a "32.87%" string into 32.87. The unit
// suffix is required (pprof always emits one); a missing suffix
// is a signal that the field is not a percent column.
func parsePProfPercent(s string) (float64, error) {
	if !strings.HasSuffix(s, "%") {
		return 0, fmt.Errorf("not a percent: %q", s)
	}
	return strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
}
