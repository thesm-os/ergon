// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package doctor

import (
	"context"
	"errors"
	"io"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/bootstrap"
	xexec "go.thesmos.sh/ergon/internal/exec"
)

// TestRun pins the top-level shape: every binary on PATH plus a
// matching Go toolchain renders PASS; a single missing tool
// renders FAIL and the function returns a non-nil error.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("all tools present + matching go version returns nil", func(t *testing.T) {
		t.Parallel()
		root := writeGoMod(t, "1.26.3")
		runner := &fakeRunner{
			present:   allBinaries(),
			goVersion: "go version go1.26.3 darwin/arm64",
		}
		var stdout strings.Builder
		if err := Run(t.Context(), runner, &stdout, io.Discard, root, bootstrap.Config{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		body := stdout.String()
		if !strings.Contains(body, "✓ PASS") {
			t.Fatalf("stdout missing PASS marks: %q", body)
		}
		if !strings.Contains(body, "every required binary is on PATH") {
			t.Fatalf("stdout missing pass message: %q", body)
		}
	})

	t.Run("missing binary returns error and lists install command", func(t *testing.T) {
		t.Parallel()
		root := writeGoMod(t, "1.26.3")
		present := allBinaries()
		delete(present, "govulncheck")
		runner := &fakeRunner{
			present:   present,
			goVersion: "go version go1.26.3 darwin/arm64",
		}
		var stdout strings.Builder
		err := Run(t.Context(), runner, &stdout, io.Discard, root, bootstrap.Config{})
		if err == nil {
			t.Fatal("Run returned nil, want error for missing govulncheck")
		}
		body := stdout.String()
		if !strings.Contains(body, "missing") {
			t.Fatalf("stdout missing install hint: %q", body)
		}
		if !strings.Contains(body, "golang.org/x/vuln/cmd/govulncheck") {
			t.Fatalf("stdout missing install package: %q", body)
		}
	})

	t.Run("go version mismatch renders skip + names both versions", func(t *testing.T) {
		t.Parallel()
		root := writeGoMod(t, "1.25.0")
		runner := &fakeRunner{
			present:   allBinaries(),
			goVersion: "go version go1.26.3 darwin/arm64",
		}
		var stdout strings.Builder
		if err := Run(t.Context(), runner, &stdout, io.Discard, root, bootstrap.Config{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		body := stdout.String()
		if !strings.Contains(body, "1.26.3") || !strings.Contains(body, "1.25.0") {
			t.Fatalf("stdout missing version diff: %q", body)
		}
	})

	t.Run("extra tools surface in the probe list", func(t *testing.T) {
		t.Parallel()
		root := writeGoMod(t, "1.26.3")
		present := allBinaries()
		runner := &fakeRunner{
			present:   present,
			goVersion: "go version go1.26.3 darwin/arm64",
		}
		extra := bootstrap.Config{ExtraTools: []bootstrap.ToolSpec{
			{Pkg: "example.com/cmd/customtool", Version: "latest"},
		}}
		var stdout strings.Builder
		err := Run(t.Context(), runner, &stdout, io.Discard, root, extra)
		if err == nil {
			t.Fatal("Run returned nil, want missing-customtool error")
		}
		if !strings.Contains(stdout.String(), "customtool") {
			t.Fatalf("stdout did not surface the extra tool: %q", stdout.String())
		}
	})
}

// allBinaries returns the present-set used by the happy-path
// subtests: every binary the default tool list ships, plus
// markdownlint-cli2.
func allBinaries() map[string]string {
	present := map[string]string{markdownlintBinary: "/usr/local/bin/" + markdownlintBinary}
	for _, t := range bootstrap.DefaultTools {
		name := path.Base(t.Pkg)
		present[name] = "/usr/local/bin/" + name
	}
	return present
}

// writeGoMod creates a temp dir with a go.mod that declares the
// given Go version. Returns the dir.
func writeGoMod(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	body := "module example.com/x\n\ngo " + version + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return root
}

// fakeRunner satisfies [xexec.Runner] for the doctor tests.
// LookPath returns the path stored in `present`, or
// osexec.ErrNotFound. Run is used only by [probeGo]; it writes
// `goVersion` to opts.Stdout and returns nil.
type fakeRunner struct {
	present   map[string]string
	goVersion string
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	if opts.Stdout != nil && f.goVersion != "" {
		_, _ = opts.Stdout.Write([]byte(f.goVersion))
	}
	return nil
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if p, ok := f.present[name]; ok {
		return p, nil
	}
	return "", errors.Join(osexec.ErrNotFound, errors.New(name+" not found in $PATH"))
}
