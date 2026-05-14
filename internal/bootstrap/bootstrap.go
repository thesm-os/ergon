// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// VersionLatest is the canonical "track the upstream HEAD" tag
// passed to `go install`. ergon prefers `latest` over pinned semver
// for development tools so contributors pick up upstream fixes
// without manual coordination.
const VersionLatest = "latest"

// DefaultTools is the built-in tool list [Run] installs before
// processing any per-repo extras. Membership covers every binary
// ergon shells out to: gofumpt and gci handle formatting,
// golangci-lint is the umbrella linter, govulncheck powers
// `ergon check vuln`, go-license applies SPDX headers, benchstat
// backs `ergon bench regression`, gremlins backs
// `ergon check mutation`, and gobco backs `ergon check branch`.
var DefaultTools = []ToolSpec{
	{Pkg: "mvdan.cc/gofumpt", Version: VersionLatest},
	{Pkg: "github.com/daixiang0/gci", Version: VersionLatest},
	{Pkg: "github.com/golangci/golangci-lint/v2/cmd/golangci-lint", Version: VersionLatest},
	{Pkg: "golang.org/x/vuln/cmd/govulncheck", Version: VersionLatest},
	{Pkg: "github.com/palantir/go-license", Version: VersionLatest},
	{Pkg: "golang.org/x/perf/cmd/benchstat", Version: VersionLatest},
	{Pkg: "github.com/go-gremlins/gremlins/cmd/gremlins", Version: VersionLatest},
	{Pkg: "github.com/rillig/gobco", Version: VersionLatest},
}

// markdownlintHint is surfaced when neither markdownlint-cli2 nor
// npm is on PATH — the user has to install markdownlint-cli2 by
// some other means (Homebrew on macOS, distro package on Linux).
const markdownlintHint = "markdownlint-cli2 not installed; install with: " +
	"brew install markdownlint-cli2 (or npm install -g markdownlint-cli2)"

// Run installs every entry in [DefaultTools] followed by the
// per-repo extras from cfg.ExtraTools, applying cfg.Pinned as a
// per-package version override across the combined list, then
// probes for markdownlint-cli2 and tries to install it via npm
// when missing.
//
// Behaviour:
//
//   - A failing `go install` aborts the run; the partial state is
//     left in $GOBIN for the user to inspect.
//   - A missing markdownlint-cli2 with no npm available surfaces
//     as a warning on stderr, not an error — the rest of the tool
//     list installed successfully, and the user can install
//     markdownlint by hand.
//   - Pinned entries naming an unknown package are silently
//     ignored. The surface is additive: pinning a tool before
//     adding it to ExtraTools is legal.
//
// stdout receives progress messages (one line per tool); stderr
// carries the warning case described above plus any subprocess
// noise.
func Run(ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer, cfg Config) error {
	all := resolveTools(cfg)
	for _, t := range all {
		if err := installGoTool(ctx, runner, stdout, stderr, t); err != nil {
			return fmt.Errorf("install %s: %w", t.Pkg, err)
		}
	}

	if err := ensureMarkdownlint(ctx, runner, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "warning:", err)
	}
	return nil
}

// resolveTools merges [DefaultTools] with cfg.ExtraTools and
// applies cfg.Pinned as a per-package version override. Exposed
// at package scope so the merge rules — declaration order, the
// "Pinned beats the per-spec Version" precedence, the silent
// drop of pins that name no installed package — are independently
// testable without exercising the subprocess machinery.
func resolveTools(cfg Config) []ToolSpec {
	all := make([]ToolSpec, 0, len(DefaultTools)+len(cfg.ExtraTools))
	all = append(all, DefaultTools...)
	all = append(all, cfg.ExtraTools...)
	for i := range all {
		if v, ok := cfg.Pinned[all[i].Pkg]; ok && v != "" {
			all[i].Version = v
		}
	}
	return all
}

// installGoTool runs `go install <Pkg>@<Version>` for the given
// spec. An empty Version is treated as [VersionLatest].
func installGoTool(ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer, t ToolSpec) error {
	version := t.Version
	if version == "" {
		version = VersionLatest
	}
	fmt.Fprintf(stdout, "  installing %s@%s\n", t.Pkg, version)
	return runner.Run(ctx,
		xexec.Options{Stdout: stdout, Stderr: stderr},
		"go", "install", fmt.Sprintf("%s@%s", t.Pkg, version))
}

// ensureMarkdownlint returns nil when markdownlint-cli2 is already
// on PATH or successfully installs via npm. When neither
// markdownlint-cli2 nor npm is available, returns an error
// carrying [markdownlintHint] for the caller to surface as a
// warning.
func ensureMarkdownlint(ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer) error {
	if _, err := runner.LookPath("markdownlint-cli2"); err == nil {
		return nil
	}
	if _, err := runner.LookPath("npm"); err != nil {
		return errors.New(markdownlintHint)
	}
	fmt.Fprintln(stdout, "  installing markdownlint-cli2 via npm")
	return runner.Run(ctx,
		xexec.Options{Stdout: stdout, Stderr: stderr},
		"npm", "install", "-g", "markdownlint-cli2")
}
