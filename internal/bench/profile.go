// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/style"
)

// ProfileOptions carries the per-invocation overrides the cobra
// layer passes to [Profile]. Defaults are chosen for a quick
// interactive run; long-form profiling for CI lives in
// [Baseline] / [Regression] which write benchstat-compatible
// numbers instead.
type ProfileOptions struct {
	// Pattern is the benchmark-name regex passed to `go test
	// -bench`. Empty means `.` (every benchmark in the package).
	Pattern string

	// Module is the module directory to run inside (the cwd of the
	// `go test` invocation). Empty means the repository root.
	Module string

	// Packages is the package pattern within the module. Empty
	// means `./...`. The pattern is expanded via `go list` so
	// each matching package gets its own profile output dir —
	// `-cpuprofile=PATH` is per-test-binary, and a wildcard that
	// matches multiple packages would otherwise clobber the same
	// file across runs.
	Packages string

	// BenchTime is `-benchtime`. A longer value yields a more
	// statistically meaningful profile at the cost of wall time.
	// Zero means [DefaultBenchTime].
	BenchTime time.Duration

	// Count is `-count`. Profiling typically wants 1; bench
	// stability benefits from higher counts.
	Count int

	// CPU enables `-cpuprofile`.
	CPU bool

	// Mem enables `-memprofile -benchmem`.
	Mem bool

	// Block enables `-blockprofile`. Captures goroutine-blocking
	// events (channel waits, mutex contention, syscalls). Off by
	// default — adds runtime overhead.
	Block bool

	// Mutex enables `-mutexprofile`. Captures mutex contention
	// events. Off by default — adds runtime overhead.
	Mutex bool

	// OutputDir is the absolute directory the profile artefacts
	// are written under. Each profiled package gets its own
	// `<slug>/` subdirectory inside; the caller composes the
	// parent path (typically `<root>/.<project>/profiles`).
	OutputDir string

	// TopN bounds the per-artefact pprof summary table size.
	// Zero means [DefaultProfileTopN].
	TopN int
}

// DefaultProfileTopN is the default `-nodecount=N` value the
// pprof summarizer uses when [ProfileOptions.TopN] is zero. Ten
// rows is enough to surface the hot spot without scrolling.
const DefaultProfileTopN = 10

// DefaultBenchTime is the `-benchtime` value Profile uses when
// [ProfileOptions.BenchTime] is zero. Five seconds is long enough
// to surface stable hot spots in a typical benchmark without
// turning an interactive `ergon bench profile` invocation into a
// minutes-long wait.
const DefaultBenchTime = 5 * time.Second

// ErrNoProfilesRequested signals that all of ProfileOptions.{CPU,
// Mem, Block, Mutex} were false — the caller asked for no
// artefact, which makes the run pointless.
var ErrNoProfilesRequested = errors.New("bench: at least one of --cpu / --mem / --block / --mutex must be set")

// Profile runs `go test -bench=<pattern>` against each package
// matching opts.Packages with the requested pprof artefacts
// collected. Each package gets its own `<slug>/` subdirectory
// under opts.OutputDir so `-cpuprofile`-style flags do not
// clobber each other when the pattern matches multiple packages.
// The per-package output is parsed and rendered as a styled
// table; the raw `go test -bench` chatter is suppressed unless
// the run fails (in which case the captured output is revealed
// indented under the failing verdict).
//
// Unlike [Baseline] / [Regression], Profile is interactive — it
// expects a benchmark pattern, not a per-module fan-out.
func Profile(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, opts ProfileOptions,
) error {
	if !opts.CPU && !opts.Mem && !opts.Block && !opts.Mutex {
		return ErrNoProfilesRequested
	}
	opts = profileWithDefaults(opts)

	s := style.Detect(stdout)
	s.Header(stdout, "bench profile", profileHeaderDetails(opts))

	if err := os.MkdirAll(opts.OutputDir, 0o700); err != nil {
		return fmt.Errorf("bench: create profile dir: %w", err)
	}

	moduleDir := filepath.Join(root, opts.Module)
	pkgs, err := listPackages(ctx, runner, moduleDir, opts.Packages)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "  %s\n", s.Dimmed("— no packages match "+opts.Packages))
		fmt.Fprintln(stdout)
		return nil
	}

	totalArtefacts := 0
	totalBenches := 0
	var failures []error
	for _, pkg := range pkgs {
		report, perr := runPackageProfile(ctx, runner, moduleDir, pkg, opts)
		if perr != nil {
			failures = append(failures, fmt.Errorf("[%s]: %w", pkg, perr))
		}
		writeProfileSection(stdout, s, report)
		totalArtefacts += len(report.Artefacts)
		totalBenches += len(report.Rows)
	}

	fmt.Fprintln(stdout)
	if len(failures) > 0 {
		s.FinalVerdict(stdout, false,
			fmt.Sprintf("%d package(s) failed during profiling", len(failures)))
		fmt.Fprintln(stdout)
		return errors.Join(failures...)
	}
	s.FinalVerdict(stdout, true,
		fmt.Sprintf("%d package(s) profiled · %d benchmark(s) · %d artefact(s)",
			len(pkgs), totalBenches, totalArtefacts))
	fmt.Fprintln(stdout)
	return nil
}

// packageReport bundles everything one per-package run renders:
// the import path, the parsed benchmark rows, the artefact paths
// written, the per-artefact pprof summaries, and the captured
// raw output (kept for the failure path so callers see the
// underlying error).
type packageReport struct {
	Pkg       string
	Env       Env
	Rows      []Row
	Artefacts []profileArtefact
	Summaries []ProfileSummary
	Captured  string
	Err       error
}

// runPackageProfile runs `go test -bench` against one package
// with its own per-package artefact directory and returns the
// captured + parsed report.
func runPackageProfile(
	ctx context.Context, runner xexec.Runner,
	moduleDir, pkg string, opts ProfileOptions,
) (packageReport, error) {
	slug := packageSlug(pkg)
	pkgOutDir := filepath.Join(opts.OutputDir, slug)
	if err := os.MkdirAll(pkgOutDir, 0o700); err != nil {
		return packageReport{Pkg: pkg, Err: err},
			fmt.Errorf("bench: create %s: %w", pkgOutDir, err)
	}

	artefacts := plannedArtefacts(opts, pkgOutDir)
	args := append([]string{
		"test",
		"-bench=" + opts.Pattern,
		"-run=^$",
		"-benchtime=" + opts.BenchTime.String(),
		"-count=" + strconv.Itoa(opts.Count),
	}, artefactFlags(artefacts)...)
	args = append(args, pkg)

	var captured bytes.Buffer
	runErr := runner.Run(ctx,
		xexec.Options{Dir: moduleDir, Stdout: &captured, Stderr: &captured},
		"go", args...)

	env, rows := parseBenchOutput(captured.String())
	written := keepWrittenArtefacts(artefacts)
	report := packageReport{
		Pkg:       pkg,
		Env:       env,
		Rows:      rows,
		Artefacts: written,
		Captured:  captured.String(),
		Err:       runErr,
	}
	if runErr != nil {
		return report, runErr
	}
	topN := opts.TopN
	if topN == 0 {
		topN = DefaultProfileTopN
	}
	for _, a := range written {
		sum, sumErr := summarizeProfile(ctx, runner, moduleDir, a.Kind, a.Path, topN)
		if sumErr != nil {
			// A pprof failure is not fatal for the whole run —
			// the artefact is on disk and the user can open it
			// manually. Surface the error in the captured field
			// alongside the existing chatter.
			report.Captured += "\n" + sumErr.Error()
			continue
		}
		report.Summaries = append(report.Summaries, sum)
	}
	return report, nil
}

// keepWrittenArtefacts filters the planned artefacts down to the
// ones that actually exist on disk after the bench run. Go skips
// writing a profile when the run produced zero benchmark
// samples; surfacing a dead path in the summary would mislead
// the reader.
func keepWrittenArtefacts(artefacts []profileArtefact) []profileArtefact {
	out := make([]profileArtefact, 0, len(artefacts))
	for _, a := range artefacts {
		if info, err := os.Stat(a.Path); err == nil && info.Size() > 0 {
			out = append(out, a)
		}
	}
	return out
}

// listPackages expands the package pattern via `go list`. The
// result is the set of import paths the bench run iterates;
// each gets its own artefact subdirectory.
func listPackages(
	ctx context.Context, runner xexec.Runner, dir, pattern string,
) ([]string, error) {
	var out bytes.Buffer
	if err := runner.Run(ctx,
		xexec.Options{Dir: dir, Stdout: &out, Stderr: &out},
		"go", "list", "-f", "{{.ImportPath}}", pattern); err != nil {
		return nil, fmt.Errorf("bench: go list %s: %w: %s",
			pattern, err, strings.TrimSpace(out.String()))
	}
	var pkgs []string
	for line := range strings.SplitSeq(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkgs = append(pkgs, line)
	}
	return pkgs, nil
}

// packageSlug derives a filesystem-safe directory name from a
// package import path. The full path is preserved (slashes
// flattened to underscores) so two leaf packages with the same
// basename do not collide.
func packageSlug(pkg string) string {
	slug := strings.ReplaceAll(pkg, "/", "_")
	slug = strings.ReplaceAll(slug, ":", "_")
	return slug
}

// profileArtefact records one collected profile.
type profileArtefact struct {
	// Kind is the human-facing name (cpu / mem / block / mutex).
	Kind string
	// Flag is the `-Xprofile=` flag passed to `go test`.
	Flag string
	// Path is the absolute path the artefact lands at.
	Path string
}

// plannedArtefacts returns the profile artefacts enabled by
// opts, scoped to the per-package output directory.
func plannedArtefacts(opts ProfileOptions, dir string) []profileArtefact {
	var out []profileArtefact
	if opts.CPU {
		out = append(out, profileArtefact{
			Kind: "cpu", Flag: "-cpuprofile",
			Path: filepath.Join(dir, "cpu.prof"),
		})
	}
	if opts.Mem {
		out = append(out, profileArtefact{
			Kind: "mem", Flag: "-memprofile",
			Path: filepath.Join(dir, "mem.prof"),
		})
	}
	if opts.Block {
		out = append(out, profileArtefact{
			Kind: "block", Flag: "-blockprofile",
			Path: filepath.Join(dir, "block.prof"),
		})
	}
	if opts.Mutex {
		out = append(out, profileArtefact{
			Kind: "mutex", Flag: "-mutexprofile",
			Path: filepath.Join(dir, "mutex.prof"),
		})
	}
	return out
}

// artefactFlags translates the planned artefacts into the
// `-Xprofile=PATH` args `go test` accepts. `-benchmem` is added
// alongside `-memprofile` so the allocation columns appear in
// the bench output too.
func artefactFlags(artefacts []profileArtefact) []string {
	args := make([]string, 0, len(artefacts)*2+1)
	for _, a := range artefacts {
		args = append(args, a.Flag+"="+a.Path)
		if a.Kind == "mem" {
			args = append(args, "-benchmem")
		}
	}
	return args
}

// profileHeaderDetails composes the header's details column —
// the canonical bench flags + ergon's defaults so the reader
// knows what was actually run without reading the cobra flag
// table.
func profileHeaderDetails(opts ProfileOptions) string {
	return fmt.Sprintf("-bench=%s · -benchtime=%s · -count=%d",
		opts.Pattern, opts.BenchTime, opts.Count)
}

// writeProfileSection renders one package's report: an env line,
// a benchmark table, the artefact list with `go tool pprof`
// invocations, and (on failure) the captured raw output
// indented under a FAIL verdict.
func writeProfileSection(w io.Writer, s style.Style, r packageReport) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", s.Bolded(r.Pkg))
	if env := envOneLine(r.Env); env != "" {
		fmt.Fprintf(w, "    %s\n", s.Dimmed(env))
	}

	if r.Err != nil {
		fmt.Fprintf(w, "    %s   %v\n", s.Fail(), r.Err)
		if body := strings.TrimRight(r.Captured, "\n"); body != "" {
			fmt.Fprintln(w, s.Dimmed(style.Indent(body, "      ")))
		}
		return
	}

	if len(r.Rows) == 0 {
		fmt.Fprintf(w, "    %s\n", s.Dimmed("— no benchmarks matched the pattern"))
		return
	}

	writeBenchTable(w, r.Rows)
	if len(r.Artefacts) == 0 {
		return
	}
	summariesByKind := make(map[string]ProfileSummary, len(r.Summaries))
	for _, sum := range r.Summaries {
		summariesByKind[sum.Kind] = sum
	}
	for _, a := range r.Artefacts {
		fmt.Fprintln(w)
		header := fmt.Sprintf("%s profile", a.Kind)
		if sum, ok := summariesByKind[a.Kind]; ok && sum.Total != "" {
			header = fmt.Sprintf("%s profile · %s total", a.Kind, sum.Total)
		}
		fmt.Fprintf(w, "    %s\n", s.Bolded(header))
		if sum, ok := summariesByKind[a.Kind]; ok && len(sum.Rows) > 0 {
			writePProfTable(w, s, sum.Rows)
		} else {
			fmt.Fprintf(w, "      %s\n", s.Dimmed("— no samples"))
		}
		fmt.Fprintf(w, "      %s\n",
			s.Dimmed("go tool pprof -http=: "+a.Path))
	}
}

// writePProfTable renders the per-row pprof table aligned in
// fixed columns. Flat / Cum keep pprof's native unit suffixes
// (`ms`, `kB`, …) verbatim; the percent columns format to two
// decimal places for stability under floating noise.
func writePProfTable(w io.Writer, s style.Style, rows []ProfileRow) {
	flatWidth, cumWidth, symWidth := 4, 4, 6 // header widths
	for _, r := range rows {
		flatWidth = max(flatWidth, len(r.Flat))
		cumWidth = max(cumWidth, len(r.Cum))
		symWidth = max(symWidth, len(r.Symbol))
	}
	headerFmt := "      %*s  %7s   %*s  %7s   %-*s\n"
	rowFmt := "      %*s  %6.2f%%   %*s  %6.2f%%   %-*s\n"
	fmt.Fprintf(w, headerFmt,
		flatWidth, "flat", "flat%",
		cumWidth, "cum", "cum%",
		symWidth, "symbol")
	for _, r := range rows {
		fmt.Fprintf(w, rowFmt,
			flatWidth, r.Flat, r.FlatPct,
			cumWidth, r.Cum, r.CumPct,
			symWidth, s.Dimmed(r.Symbol))
	}
}

// envOneLine collapses the four env fields into a compact
// one-line summary suitable for the dimmed env row beneath the
// package name. Empty fields are dropped.
func envOneLine(env Env) string {
	parts := make([]string, 0, 4)
	if env.GOOS != "" && env.GOARCH != "" {
		parts = append(parts, env.GOOS+"/"+env.GOARCH)
	}
	if env.CPU != "" {
		parts = append(parts, env.CPU)
	}
	return strings.Join(parts, " · ")
}

// writeBenchTable renders the per-row bench results aligned in
// fixed columns. Right-aligned numbers, left-aligned name. ns/op
// values switch unit (ns → µs → ms) based on magnitude so the
// column stays compact for fast benchmarks and readable for slow
// ones.
func writeBenchTable(w io.Writer, rows []Row) {
	nameWidth := len("Benchmark")
	for _, r := range rows {
		if n := len(r.Name); n > nameWidth {
			nameWidth = n
		}
	}
	for _, r := range rows {
		fmt.Fprintf(w, "    %-*s   %12s iters   %10d ns/op   %8d B/op   %4d allocs/op\n",
			nameWidth, r.Name,
			humanIters(r.N),
			int64(r.NsPerOp),
			r.BPerOp, r.Allocs)
	}
}

// humanIters formats the iteration count with thousands grouping
// (1,830,078) — long counts otherwise drown the rest of the
// row in digits.
func humanIters(n int64) string {
	s := strconv.FormatInt(n, 10)
	var out strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	return out.String()
}

// profileWithDefaults fills any zero-value field on opts so the
// caller may pass an empty struct and still get a runnable
// configuration.
func profileWithDefaults(opts ProfileOptions) ProfileOptions {
	if opts.Pattern == "" {
		opts.Pattern = "."
	}
	if opts.Packages == "" {
		opts.Packages = "./..."
	}
	if opts.BenchTime == 0 {
		opts.BenchTime = DefaultBenchTime
	}
	if opts.Count == 0 {
		opts.Count = 1
	}
	return opts
}
