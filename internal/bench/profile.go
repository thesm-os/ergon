// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	// means `./...`.
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
	// are written to. The caller composes the path (typically
	// `<root>/.<project>/profiles`); Profile creates it on demand.
	OutputDir string
}

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

// Profile runs `go test -bench=<pattern>` against one module's
// package set with the requested profile artefacts collected.
// The artefacts are written to opts.OutputDir; the section
// summary names each artefact and the `go tool pprof` command
// that opens it.
//
// Unlike [Baseline] / [Regression], Profile targets a single
// module and a single benchmark pattern — profiling is an
// interactive operation, not a per-module fan-out. The bench
// run uses `-run=^$` so non-benchmark tests do not pad the
// profile.
func Profile(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, opts ProfileOptions,
) error {
	if !opts.CPU && !opts.Mem && !opts.Block && !opts.Mutex {
		return ErrNoProfilesRequested
	}
	opts = profileWithDefaults(opts)

	s := style.Detect(stdout)
	s.Header(stdout, "bench profile",
		fmt.Sprintf("collect pprof artefacts (-bench=%s, -benchtime=%s)",
			opts.Pattern, opts.BenchTime))

	if err := os.MkdirAll(opts.OutputDir, 0o700); err != nil {
		return fmt.Errorf("bench: create profile dir: %w", err)
	}

	artefacts := plannedArtefacts(opts)
	args := append([]string{
		"test",
		"-bench=" + opts.Pattern,
		"-run=^$",
		"-benchtime=" + opts.BenchTime.String(),
		"-count=" + strconv.Itoa(opts.Count),
	}, artefactFlags(artefacts)...)
	args = append(args, opts.Packages)

	cwd := filepath.Join(root, opts.Module)
	if err := runner.Run(ctx,
		xexec.Options{Dir: cwd, Stdout: stdout, Stderr: stderr},
		"go", args...); err != nil {
		return fmt.Errorf("bench: go test -bench: %w", err)
	}

	renderProfileSummary(stdout, s, artefacts)
	return nil
}

// profileArtefact records one collected profile so the summary
// renderer can print a `go tool pprof` command for it.
type profileArtefact struct {
	// Kind is the human-facing name (cpu / mem / block / mutex).
	Kind string
	// Flag is the `-Xprofile=` flag passed to `go test`.
	Flag string
	// Path is the absolute path the artefact lands at.
	Path string
}

// plannedArtefacts returns the profile artefacts enabled by
// opts, in stable order so the rendered summary is reproducible.
func plannedArtefacts(opts ProfileOptions) []profileArtefact {
	var out []profileArtefact
	if opts.CPU {
		out = append(out, profileArtefact{
			Kind: "cpu", Flag: "-cpuprofile",
			Path: filepath.Join(opts.OutputDir, "cpu.prof"),
		})
	}
	if opts.Mem {
		out = append(out, profileArtefact{
			Kind: "mem", Flag: "-memprofile",
			Path: filepath.Join(opts.OutputDir, "mem.prof"),
		})
	}
	if opts.Block {
		out = append(out, profileArtefact{
			Kind: "block", Flag: "-blockprofile",
			Path: filepath.Join(opts.OutputDir, "block.prof"),
		})
	}
	if opts.Mutex {
		out = append(out, profileArtefact{
			Kind: "mutex", Flag: "-mutexprofile",
			Path: filepath.Join(opts.OutputDir, "mutex.prof"),
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

// renderProfileSummary writes the per-artefact summary block:
// each artefact lists its path and the `go tool pprof -http=:`
// command that opens it in a browser.
func renderProfileSummary(
	w io.Writer, s style.Style, artefacts []profileArtefact,
) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", s.Bolded("Profile artefacts"))
	for _, a := range artefacts {
		fmt.Fprintf(w, "    %s   %s\n", a.Kind, a.Path)
		fmt.Fprintf(w, "      %s\n",
			s.Dimmed(fmt.Sprintf("go tool pprof -http=: %s", a.Path)))
	}
	fmt.Fprintln(w)
	s.FinalVerdict(w, true,
		fmt.Sprintf("%d profile(s) collected", len(artefacts)))
	fmt.Fprintln(w)
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
