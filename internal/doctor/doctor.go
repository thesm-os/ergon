// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package doctor probes the local environment for the binaries
// every ergon gate expects and reports the result as a styled
// table.
//
// Two probes run:
//
//   - PATH lookup for each binary in [bootstrap.DefaultTools] plus
//     `cfg.ExtraTools` plus `markdownlint-cli2` (special-cased
//     because it ships via npm/Homebrew, not `go install`).
//   - The Go toolchain version reported by `go version` compared
//     against the `go` directive in the repository's `go.mod`.
//
// The command exits non-zero when any required binary is missing
// so CI environments can gate on a clean doctor report.
package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.thesmos.sh/ergon/internal/bootstrap"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/style"
)

// markdownlintBinary is the binary the markdown subsystem invokes
// when present. The doctor probes for it separately from the Go
// tool list because [bootstrap.Run] installs it via npm rather
// than `go install`.
const markdownlintBinary = "markdownlint-cli2"

// goModVersionPattern matches the `go <semver>` directive at the
// top of `go.mod`. Used to compare the toolchain the user runs
// against the toolchain the module declares it needs.
var goModVersionPattern = regexp.MustCompile(`(?m)^go\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)\b`)

// goVersionPattern matches the `go1.26.3` token in the output of
// `go version`. The full output also carries the platform suffix
// (e.g. `go version go1.26.3 darwin/arm64`); the doctor cares
// about the version string only.
var goVersionPattern = regexp.MustCompile(`go([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)

// Run probes every required binary and prints a styled table.
// Returns an error when at least one required binary is missing
// so CI environments can gate on a clean report.
func Run(
	ctx context.Context, runner xexec.Runner,
	stdout, stderr io.Writer, root string, cfg bootstrap.Config,
) error {
	_ = stderr // doctor never writes findings to stderr; everything renders to stdout.
	s := style.Detect(stdout)
	s.Header(stdout, "doctor", "verify the local environment is fit for ergon")

	results := probeBinaries(runner, cfg)
	results = append(results, probeGo(ctx, runner, root))

	pass := "every required binary is on PATH and the toolchain matches go.mod"
	fail := countMissing(results)
	failed := s.Summary(stdout, results, pass, fail)
	if failed {
		return errors.New("doctor: one or more required tools missing")
	}
	return nil
}

// probeBinaries returns one [style.StageResult] per binary in the
// canonical tool list. A binary that resolves on PATH renders as
// PASS with its resolved path in the note column; a missing one
// renders as FAIL with the `go install` invocation that would
// fix it (or the markdownlint hint).
func probeBinaries(runner xexec.Runner, cfg bootstrap.Config) []style.StageResult {
	tools := make([]bootstrap.ToolSpec, 0, len(bootstrap.DefaultTools)+len(cfg.ExtraTools))
	tools = append(tools, bootstrap.DefaultTools...)
	tools = append(tools, cfg.ExtraTools...)

	results := make([]style.StageResult, 0, len(tools)+1)
	for _, t := range tools {
		results = append(results, probeBinary(runner, t))
	}
	results = append(results, probeMarkdownlint(runner))
	return results
}

// probeBinary returns the [style.StageResult] for one
// [bootstrap.ToolSpec]: PASS with the resolved path on hit,
// FAIL with the canonical install command on miss.
func probeBinary(runner xexec.Runner, t bootstrap.ToolSpec) style.StageResult {
	binary := path.Base(t.Pkg)
	full, err := runner.LookPath(binary)
	if err != nil {
		version := t.Version
		if version == "" {
			version = bootstrap.VersionLatest
		}
		return style.StageResult{
			Label: binary,
			Err:   err,
			Note:  fmt.Sprintf("missing — install with: go install %s@%s", t.Pkg, version),
		}
	}
	return style.StageResult{Label: binary, Note: full}
}

// probeMarkdownlint produces the [style.StageResult] for
// markdownlint-cli2. The binary is not installable via `go
// install`, so a miss surfaces the same hint the bootstrap
// subsystem uses.
func probeMarkdownlint(runner xexec.Runner) style.StageResult {
	full, err := runner.LookPath(markdownlintBinary)
	if err != nil {
		return style.StageResult{
			Label: markdownlintBinary,
			Err:   err,
			Note:  "missing — install via Homebrew (brew install markdownlint-cli2) or npm",
		}
	}
	return style.StageResult{Label: markdownlintBinary, Note: full}
}

// probeGo compares the Go toolchain `go version` reports against
// the `go <version>` directive in the repository's `go.mod`. A
// mismatch is surfaced as a soft skip (the project may
// intentionally target an older Go); a missing `go.mod` or an
// unparseable directive renders the probe as PASS with the raw
// `go version` output so the user can still read it.
func probeGo(ctx context.Context, runner xexec.Runner, root string) style.StageResult {
	installed, err := readGoVersion(ctx, runner, root)
	if err != nil {
		return style.StageResult{
			Label: "go",
			Err:   err,
			Note:  "could not run `go version`: " + err.Error(),
		}
	}
	required, ok := readGoModVersion(root)
	if !ok {
		return style.StageResult{Label: "go", Note: installed}
	}
	if installed == required {
		return style.StageResult{Label: "go", Note: installed + " (matches go.mod)"}
	}
	return style.StageResult{
		Label:   "go",
		Skipped: true,
		Note:    fmt.Sprintf("installed %s, go.mod declares %s", installed, required),
	}
}

// readGoVersion shells out to `go version` and returns the bare
// semver token (e.g. `1.26.3`) from the canonical output line.
func readGoVersion(ctx context.Context, runner xexec.Runner, root string) (string, error) {
	var buf bytes.Buffer
	if err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &buf, Stderr: &buf},
		"go", "version"); err != nil {
		return "", err
	}
	match := goVersionPattern.FindStringSubmatch(strings.TrimSpace(buf.String()))
	if len(match) < 2 {
		return "", fmt.Errorf("unparseable `go version` output: %q", buf.String())
	}
	return match[1], nil
}

// readGoModVersion reads the `go <version>` directive from the
// root `go.mod`. Returns ok=false when the file is missing or the
// directive cannot be parsed — both are non-fatal cases the
// caller handles by skipping the version comparison.
func readGoModVersion(root string) (string, bool) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", false
	}
	match := goModVersionPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", false
	}
	return match[1], true
}

// countMissing composes the summary failure message naming how
// many of len(results) probes failed. Empty when no probe failed
// (the renderer falls back to passMessage).
func countMissing(results []style.StageResult) string {
	missing := 0
	for _, r := range results {
		if r.Err != nil {
			missing++
		}
	}
	if missing == 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d tool(s) missing or broken", missing, len(results))
}
