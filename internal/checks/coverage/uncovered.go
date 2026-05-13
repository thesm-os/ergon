// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package coverage

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/style"
)

// UncoveredBlock records one `count=0` region from the merged
// coverprofile: the source span and the number of statements it
// contains. The path is held outside the struct by the caller
// (results are grouped by file before rendering).
type UncoveredBlock struct {
	// StartLine is the first line of the block (inclusive).
	StartLine int

	// EndLine is the last line of the block (inclusive).
	EndLine int

	// Stmts is the number of statements in the block as
	// `go tool cover` reports it.
	Stmts int
}

// Uncovered renders every `count=0` block in the merged
// coverprofile, grouped by file, regardless of which layer
// (if any) the file falls under. Unlike [Run] it does NOT
// consult cfg.Packages, the shared excludes, or the shared
// skips — the command is a debugging aid for closing coverage
// gaps, not a gate.
//
// Run `ergon test` first to produce the per-module `.out`
// profiles; Uncovered merges them in memory and walks the
// result.
//
// modulePrefix is stripped from every path so the report shows
// repo-relative paths matching what editors and CI logs use.
func Uncovered(
	_ context.Context, _ xexec.Runner, stdout, stderr io.Writer,
	_, coverageDir, modulePrefix string,
) error {
	_ = stderr
	s := style.Detect(stdout)
	s.Header(stdout, "coverage uncovered",
		"every count=0 block, ignoring layer config and exclusions")

	profiles, err := findProfiles(coverageDir)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return fmt.Errorf("coverage: no coverprofiles found in %s — run `ergon test` first", coverageDir)
	}
	_, mergedBody, cleanup, err := mergeProfiles(profiles)
	if err != nil {
		return err
	}
	defer cleanup()

	byFile := collectUncoveredBlocks(mergedBody, modulePrefix)
	renderUncovered(stdout, s, byFile)
	return nil
}

// collectUncoveredBlocks parses merged into a map keyed by
// repo-relative file path. modulePrefix is stripped from every
// path token so the keys match what the rest of ergon prints.
// Blocks within each file are sorted by start line.
func collectUncoveredBlocks(merged, modulePrefix string) map[string][]UncoveredBlock {
	out := map[string][]UncoveredBlock{}
	for line := range strings.SplitSeq(merged, "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != "0" {
			continue
		}
		path, span, ok := splitProfileHead(fields[0])
		if !ok {
			continue
		}
		startEnd := strings.Split(span, ",")
		if len(startEnd) != 2 {
			continue
		}
		startLine, err := strconv.Atoi(strings.SplitN(startEnd[0], ".", 2)[0])
		if err != nil {
			continue
		}
		endLine, err := strconv.Atoi(strings.SplitN(startEnd[1], ".", 2)[0])
		if err != nil {
			continue
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		rel := strings.TrimPrefix(path, modulePrefix)
		out[rel] = append(out[rel], UncoveredBlock{
			StartLine: startLine,
			EndLine:   endLine,
			Stmts:     stmts,
		})
	}
	for _, blocks := range out {
		sort.SliceStable(blocks, func(i, j int) bool {
			return blocks[i].StartLine < blocks[j].StartLine
		})
	}
	return out
}

// renderUncovered writes the per-file uncovered-block report
// followed by a one-line aggregate. Files are sorted
// alphabetically so the output is reproducible.
func renderUncovered(
	w io.Writer, s style.Style, byFile map[string][]UncoveredBlock,
) {
	fmt.Fprintln(w)
	if len(byFile) == 0 {
		fmt.Fprintf(w, "  %s\n\n", s.Dimmed("— every covered package has zero uncovered blocks"))
		s.FinalVerdict(w, true, "no uncovered lines across the merged coverprofile")
		fmt.Fprintln(w)
		return
	}
	paths := make([]string, 0, len(byFile))
	for p := range byFile {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var totalBlocks, totalStmts int
	for _, p := range paths {
		blocks := byFile[p]
		fmt.Fprintf(w, "  %s\n", s.Bolded(p))
		for _, b := range blocks {
			fmt.Fprintf(w, "    %d-%d %s\n",
				b.StartLine, b.EndLine,
				s.Dimmed(fmt.Sprintf("(%d stmts)", b.Stmts)))
			totalBlocks++
			totalStmts += b.Stmts
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n",
		s.Dimmed(fmt.Sprintf("%d uncovered block(s) across %d file(s); %d statement(s) total",
			totalBlocks, len(paths), totalStmts)))
	fmt.Fprintln(w)
}
