// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bench

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/style"
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

// TestProfileWithDefaults pins the per-call merge: zero fields
// inherit the package defaults; non-zero fields stand.
func TestProfileWithDefaults(t *testing.T) {
	t.Parallel()

	got := profileWithDefaults(ProfileOptions{})
	if got.Pattern != "." {
		t.Errorf("Pattern = %q, want .", got.Pattern)
	}
	if got.Packages != "./..." {
		t.Errorf("Packages = %q, want ./...", got.Packages)
	}
	if got.BenchTime != DefaultBenchTime {
		t.Errorf("BenchTime = %v, want %v", got.BenchTime, DefaultBenchTime)
	}
	if got.Count != 1 {
		t.Errorf("Count = %d, want 1", got.Count)
	}

	override := ProfileOptions{Pattern: "X", Packages: "./pkg", Count: 5}
	got = profileWithDefaults(override)
	if got.Pattern != "X" || got.Packages != "./pkg" || got.Count != 5 {
		t.Errorf("non-zero overrides lost: %+v", got)
	}
}

// TestProfileHeaderDetails pins the header's details column —
// the canonical bench flags surface verbatim.
func TestProfileHeaderDetails(t *testing.T) {
	t.Parallel()

	opts := ProfileOptions{Pattern: "Foo", BenchTime: time.Second, Count: 2}
	got := profileHeaderDetails(opts)
	for _, want := range []string{"Foo", "1s", "2"} {
		if !strings.Contains(got, want) {
			t.Errorf("details %q missing %q", got, want)
		}
	}
}

// TestEnvOneLine pins the env-row formatter: empty fields drop,
// populated fields join with the canonical `·` separator.
func TestEnvOneLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		env  Env
		want string
	}{
		{Env{}, ""},
		{Env{GOOS: "darwin", GOARCH: "arm64"}, "darwin/arm64"},
		{Env{GOOS: "darwin", GOARCH: "arm64", CPU: "M4"}, "darwin/arm64 · M4"},
		{Env{CPU: "M4"}, "M4"},
	}
	for _, tc := range cases {
		if got := envOneLine(tc.env); got != tc.want {
			t.Errorf("envOneLine(%+v) = %q, want %q", tc.env, got, tc.want)
		}
	}
}

// TestPlannedArtefacts pins the flag → artefact translation: only
// enabled artefact-kinds appear, in stable order, and each path
// sits under the per-package dir.
func TestPlannedArtefacts(t *testing.T) {
	t.Parallel()

	opts := ProfileOptions{CPU: true, Mem: true}
	got := plannedArtefacts(opts, "/tmp/pkg")
	if len(got) != 2 {
		t.Fatalf("artefacts = %d, want 2", len(got))
	}
	if got[0].Kind != "cpu" || got[1].Kind != "mem" {
		t.Fatalf("kind order = %s/%s, want cpu/mem", got[0].Kind, got[1].Kind)
	}
	// filepath.ToSlash normalises Windows backslashes so the
	// prefix assertion stays portable.
	if !strings.HasPrefix(filepath.ToSlash(got[0].Path), "/tmp/pkg/") {
		t.Fatalf("path = %q, want /tmp/pkg/ prefix", got[0].Path)
	}

	full := plannedArtefacts(ProfileOptions{CPU: true, Mem: true, Block: true, Mutex: true}, "/x")
	if len(full) != 4 {
		t.Fatalf("full artefacts = %d, want 4", len(full))
	}
}

// TestArtefactFlags pins the `-Xprofile=PATH` translation plus
// the implicit `-benchmem` that rides alongside `-memprofile`.
func TestArtefactFlags(t *testing.T) {
	t.Parallel()

	artefacts := []profileArtefact{
		{Kind: "cpu", Flag: "-cpuprofile", Path: "/x/cpu.prof"},
		{Kind: "mem", Flag: "-memprofile", Path: "/x/mem.prof"},
	}
	got := artefactFlags(artefacts)
	want := []string{
		"-cpuprofile=/x/cpu.prof",
		"-memprofile=/x/mem.prof",
		"-benchmem",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("artefactFlags = %+v, want %+v", got, want)
	}
}

// TestKeepWrittenArtefacts pins the post-run filter: artefacts
// whose target file does not exist (or exists but is empty) are
// dropped from the rendered summary so dead paths never surface.
func TestKeepWrittenArtefacts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	present := filepath.Join(dir, "cpu.prof")
	if err := os.WriteFile(present, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	artefacts := []profileArtefact{
		{Kind: "cpu", Path: present},
		{Kind: "mem", Path: filepath.Join(dir, "mem.prof")}, // missing
	}
	got := keepWrittenArtefacts(artefacts)
	if len(got) != 1 || got[0].Kind != "cpu" {
		t.Fatalf("kept = %+v, want only cpu", got)
	}
}

// TestWriteBenchTable pins the per-row bench-output table the
// renderer emits beneath the package name.
func TestWriteBenchTable(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	writeBenchTable(&buf, []Row{
		{Name: "BenchmarkX-14", N: 1234, NsPerOp: 100, BPerOp: 8, Allocs: 1},
	})
	out := buf.String()
	for _, want := range []string{"BenchmarkX-14", "1,234", "100", "8", "1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %q", want, out)
		}
	}
}

// TestWritePProfTable pins the pprof-top table renderer: header
// row + one body row per [ProfileRow].
func TestWritePProfTable(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	writePProfTable(&buf, style.Style{}, []ProfileRow{
		{Flat: "1ms", FlatPct: 50, Cum: "1ms", CumPct: 50, Symbol: "pkg.Foo"},
	})
	out := buf.String()
	for _, want := range []string{"flat", "flat%", "cum", "cum%", "symbol", "1ms", "50.00", "pkg.Foo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %q", want, out)
		}
	}
}

// TestWriteProfileSection exercises the per-package report
// renderer along its three branches: clean success, "no
// benchmarks matched" notice, and the failure path that dumps
// the captured raw output.
func TestWriteProfileSection(t *testing.T) {
	t.Parallel()

	t.Run("clean success renders bench rows + artefacts", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		writeProfileSection(&buf, style.Style{}, packageReport{
			Pkg:       "pkg/a",
			Env:       Env{GOOS: "darwin"},
			Rows:      []Row{{Name: "BenchmarkX", N: 1, NsPerOp: 1}},
			Artefacts: []profileArtefact{{Kind: "cpu", Path: "/x/cpu.prof"}},
		})
		out := buf.String()
		for _, want := range []string{"pkg/a", "BenchmarkX", "cpu profile", "/x/cpu.prof"} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q: %q", want, out)
			}
		}
	})

	t.Run("no benchmarks matched renders the dimmed notice", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		writeProfileSection(&buf, style.Style{}, packageReport{Pkg: "pkg/b"})
		if !strings.Contains(buf.String(), "no benchmarks matched") {
			t.Fatalf("output missing notice: %q", buf.String())
		}
	})

	t.Run("error path reveals captured output", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		writeProfileSection(&buf, style.Style{}, packageReport{
			Pkg:      "pkg/c",
			Err:      errSentinel,
			Captured: "compile error here\n",
		})
		out := buf.String()
		if !strings.Contains(out, "FAIL") {
			t.Fatalf("output missing FAIL: %q", out)
		}
		if !strings.Contains(out, "compile error here") {
			t.Fatalf("output missing captured body: %q", out)
		}
	})
}

// TestListPackages pins the `go list -f '{{.ImportPath}}'`
// expansion: trimmed non-empty lines become the package list,
// blank lines are dropped.
func TestListPackages(t *testing.T) {
	t.Parallel()

	runner := &profileFakeRunner{stdout: "go.example.com/a\n\ngo.example.com/b\n"}
	got, err := listPackages(t.Context(), runner, "/repo", "./...")
	if err != nil {
		t.Fatalf("listPackages err: %v", err)
	}
	want := []string{"go.example.com/a", "go.example.com/b"}
	if !slices.Equal(got, want) {
		t.Fatalf("packages = %+v, want %+v", got, want)
	}
}

// TestProfile exercises the orchestrator end-to-end: missing
// --cpu/--mem/--block/--mutex returns [ErrNoProfilesRequested];
// a happy run lists every matched package and renders a
// per-package section.
func TestProfile(t *testing.T) {
	t.Parallel()

	t.Run("no profile flags returns ErrNoProfilesRequested", func(t *testing.T) {
		t.Parallel()
		err := Profile(t.Context(), &profileFakeRunner{},
			io.Discard, io.Discard, "/repo", ProfileOptions{})
		if err == nil {
			t.Fatal("Profile returned nil, want ErrNoProfilesRequested")
		}
	})

	t.Run("no matching packages renders a notice and returns nil", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runner := &profileFakeRunner{stdout: ""} // empty go list output
		var stdout strings.Builder
		err := Profile(t.Context(), runner, &stdout, io.Discard, "/repo",
			ProfileOptions{CPU: true, OutputDir: dir})
		if err != nil {
			t.Fatalf("Profile err: %v", err)
		}
		if !strings.Contains(stdout.String(), "no packages match") {
			t.Fatalf("stdout missing notice: %q", stdout.String())
		}
	})

	t.Run("happy path lists, profiles, and renders the per-package report", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Profile shells out to: go list, then go test -bench per
		// package, then go tool pprof per artefact. The fake runner
		// returns the listed packages on the first call (any
		// subsequent call sees the same stdout, but the artefact-
		// summarisation step only fires when an artefact file
		// exists on disk, so we keep the disk empty to short-
		// circuit summarisation cleanly).
		runner := &profileFakeRunner{stdout: "go.example.com/pkg\n"}
		var stdout strings.Builder
		err := Profile(t.Context(), runner, &stdout, io.Discard, "/repo",
			ProfileOptions{CPU: true, OutputDir: dir})
		if err != nil {
			t.Fatalf("Profile err: %v", err)
		}
		if !strings.Contains(stdout.String(), "go.example.com/pkg") {
			t.Fatalf("stdout missing pkg report: %q", stdout.String())
		}
	})

	t.Run("per-package failure surfaces in the final verdict", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runner := &profileSequenceRunner{
			outputs: []string{"go.example.com/pkg\n", "compile error\n"},
			errs:    []error{nil, errSentinel},
		}
		var stdout strings.Builder
		err := Profile(t.Context(), runner, &stdout, io.Discard, "/repo",
			ProfileOptions{CPU: true, OutputDir: dir})
		if err == nil {
			t.Fatal("Profile err = nil, want aggregated failure")
		}
	})
}

// profileSequenceRunner returns a per-call stdout body and error
// from parallel slices so a single test can simulate
// `go list` (call 1) succeeding and `go test -bench` (call 2)
// failing.
type profileSequenceRunner struct {
	calls   int
	outputs []string
	errs    []error
}

func (f *profileSequenceRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	idx := f.calls
	f.calls++
	if opts.Stdout != nil && idx < len(f.outputs) {
		_, _ = opts.Stdout.Write([]byte(f.outputs[idx]))
	}
	if idx < len(f.errs) {
		return f.errs[idx]
	}
	return nil
}

func (*profileSequenceRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// errSentinel is reused across the per-section subtests as a
// stand-in for any `go test` failure.
var errSentinel = sentinelError("sentinel")

// sentinelError is a typed string so the sentinel formats as
// "sentinel" inside the rendered FAIL line.
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

// profileFakeRunner satisfies [xexec.Runner] for the orchestrator
// tests. stdout is echoed to opts.Stdout (used by listPackages
// to parse the `go list` output); err is the simulated subprocess
// exit.
type profileFakeRunner struct {
	stdout string
	err    error
}

func (f *profileFakeRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	if opts.Stdout != nil && f.stdout != "" {
		_, _ = opts.Stdout.Write([]byte(f.stdout))
	}
	return f.err
}

func (*profileFakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// TestRunPackageProfile pins the per-package execution path used
// by [Profile]: runs `go test -bench` with the artefact flags,
// captures stdout, then summarises any artefacts that ended up
// on disk via `go tool pprof -top`.
func TestRunPackageProfile(t *testing.T) {
	t.Parallel()

	t.Run("happy path produces a populated report", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Pre-create the artefact that the test claims `go test`
		// would have written, so keepWrittenArtefacts retains it.
		pkgDir := filepath.Join(dir, "go.example.com_pkg")
		if err := os.MkdirAll(pkgDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "cpu.prof"),
			[]byte("x"), 0o600); err != nil {
			t.Fatalf("write cpu.prof: %v", err)
		}

		runner := &profileFakeRunner{stdout: `BenchmarkX-14   1000   100 ns/op
PASS
ok   go.example.com/pkg   1.000s
`}
		opts := profileWithDefaults(ProfileOptions{
			CPU: true, OutputDir: dir,
		})
		report, err := runPackageProfile(t.Context(), runner,
			"/repo", "go.example.com/pkg", opts)
		if err != nil {
			t.Fatalf("runPackageProfile err: %v", err)
		}
		if report.Pkg != "go.example.com/pkg" {
			t.Fatalf("Pkg = %q", report.Pkg)
		}
		if len(report.Rows) != 1 {
			t.Fatalf("Rows = %d, want 1", len(report.Rows))
		}
		if len(report.Artefacts) != 1 {
			t.Fatalf("Artefacts = %d, want 1 (cpu.prof exists)", len(report.Artefacts))
		}
	})

	t.Run("`go test` failure surfaces in the report", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runner := &profileFakeRunner{err: errSentinel, stdout: "compile error\n"}
		opts := profileWithDefaults(ProfileOptions{
			CPU: true, OutputDir: dir,
		})
		report, err := runPackageProfile(t.Context(), runner,
			"/repo", "go.example.com/pkg", opts)
		if err == nil {
			t.Fatal("err = nil, want subprocess error")
		}
		if !strings.Contains(report.Captured, "compile error") {
			t.Fatalf("Captured = %q, want it to retain failing output", report.Captured)
		}
	})
}

// TestSummarizeProfile pins the `go tool pprof -top` shell-out:
// captured stdout parses into the summary; subprocess error wraps
// with the captured body.
func TestSummarizeProfile(t *testing.T) {
	t.Parallel()

	t.Run("captured output parses into the summary", func(t *testing.T) {
		t.Parallel()
		out := `File: foo
Type: cpu
Time: ...
Duration: 1.00s, Total samples = 1000ms (100.00%)
Showing nodes accounting for 1000ms, 100% of 1000ms total
      flat  flat%   sum%        cum   cum%
    1000ms 100.00% 100.00%   1000ms 100.00%  pkg.Foo
`
		runner := &profileFakeRunner{stdout: out}
		sum, err := summarizeProfile(t.Context(), runner,
			"/repo", "cpu", "/tmp/cpu.prof", 5)
		if err != nil {
			t.Fatalf("summarizeProfile err: %v", err)
		}
		if sum.Type != "cpu" || sum.Total != "1000ms" {
			t.Fatalf("summary = %+v, want Type=cpu Total=1000ms", sum)
		}
		if len(sum.Rows) != 1 || sum.Rows[0].Symbol != "pkg.Foo" {
			t.Fatalf("rows = %+v", sum.Rows)
		}
	})

	t.Run("subprocess failure wraps the captured body", func(t *testing.T) {
		t.Parallel()
		runner := &profileFakeRunner{err: errSentinel, stdout: "pprof: cannot open\n"}
		_, err := summarizeProfile(t.Context(), runner,
			"/repo", "cpu", "/tmp/cpu.prof", 5)
		if err == nil {
			t.Fatal("err = nil, want subprocess error")
		}
		if !strings.Contains(err.Error(), "pprof: cannot open") {
			t.Fatalf("err = %v, want it to mention captured body", err)
		}
	})
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
