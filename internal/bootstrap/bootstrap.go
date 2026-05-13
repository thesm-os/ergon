// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// VersionLatest is the canonical "track the upstream HEAD" tag
// passed to `go install`. ergon prefers `latest` over pinned semver
// for development tools so contributors pick up upstream fixes
// without manual coordination.
const VersionLatest = "latest"

// DefaultTools is the built-in tool list [Run] installs before
// processing any per-repo extras. Membership reflects the union of
// what the ecosystem's Makefile templates install today: gofumpt
// and gci handle formatting, golangci-lint is the umbrella linter,
// govulncheck powers `ergon check vuln`, go-license applies SPDX
// headers, and benchstat backs `ergon bench regression`.
var DefaultTools = []ToolSpec{
	{Pkg: "mvdan.cc/gofumpt", Version: VersionLatest},
	{Pkg: "github.com/daixiang0/gci", Version: VersionLatest},
	{Pkg: "github.com/golangci/golangci-lint/v2/cmd/golangci-lint", Version: VersionLatest},
	{Pkg: "golang.org/x/vuln/cmd/govulncheck", Version: VersionLatest},
	{Pkg: "github.com/palantir/go-license", Version: VersionLatest},
	{Pkg: "golang.org/x/perf/cmd/benchstat", Version: VersionLatest},
}

// markdownlintHint is surfaced when neither markdownlint-cli2 nor
// npm is on PATH — the user has to install markdownlint-cli2 by
// some other means (Homebrew on macOS, distro package on Linux).
const markdownlintHint = "markdownlint-cli2 not installed; install with: " +
	"brew install markdownlint-cli2 (or npm install -g markdownlint-cli2)"

// runCmd shells out to a binary and streams its output to the
// caller. Package-level so tests can swap in a recorder; production
// callers always invoke the real subprocess.
var runCmd = func(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}

// lookPath wraps [exec.LookPath] behind a swappable seam so tests
// can simulate "tool present" / "tool missing" without touching
// PATH.
var lookPath = exec.LookPath

// Run installs every entry in [DefaultTools] followed by the
// per-repo extras from cfg.ExtraTools, then probes for
// markdownlint-cli2 and tries to install it via npm when missing.
//
// Behaviour:
//
//   - A failing `go install` aborts the run; the partial state is
//     left in $GOBIN for the user to inspect.
//   - A missing markdownlint-cli2 with no npm available surfaces
//     as a warning on stderr, not an error — the rest of the tool
//     list installed successfully, and the user can install
//     markdownlint by hand.
//
// stdout receives progress messages (one line per tool); stderr
// carries the warning case described above plus any subprocess
// noise.
func Run(ctx context.Context, stdout, stderr io.Writer, cfg Config) error {
	all := make([]ToolSpec, 0, len(DefaultTools)+len(cfg.ExtraTools))
	all = append(all, DefaultTools...)
	all = append(all, cfg.ExtraTools...)

	for _, t := range all {
		if err := installGoTool(ctx, stdout, stderr, t); err != nil {
			return fmt.Errorf("install %s: %w", t.Pkg, err)
		}
	}

	if err := ensureMarkdownlint(ctx, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "warning:", err)
	}
	return nil
}

// installGoTool runs `go install <Pkg>@<Version>` for the given
// spec. An empty Version is treated as [VersionLatest].
func installGoTool(ctx context.Context, stdout, stderr io.Writer, t ToolSpec) error {
	version := t.Version
	if version == "" {
		version = VersionLatest
	}
	fmt.Fprintf(stdout, "  installing %s@%s\n", t.Pkg, version)
	return runCmd(ctx, stdout, stderr, "go", "install", fmt.Sprintf("%s@%s", t.Pkg, version))
}

// ensureMarkdownlint returns nil when markdownlint-cli2 is already
// on PATH or successfully installs via npm. When neither
// markdownlint-cli2 nor npm is available, returns an error
// carrying [markdownlintHint] for the caller to surface as a
// warning.
func ensureMarkdownlint(ctx context.Context, stdout, stderr io.Writer) error {
	if _, err := lookPath("markdownlint-cli2"); err == nil {
		return nil
	}
	if _, err := lookPath("npm"); err != nil {
		return errors.New(markdownlintHint)
	}
	fmt.Fprintln(stdout, "  installing markdownlint-cli2 via npm")
	return runCmd(ctx, stdout, stderr, "npm", "install", "-g", "markdownlint-cli2")
}
