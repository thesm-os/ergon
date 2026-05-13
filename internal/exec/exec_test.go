// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// TestCommand pins the production Runner against a couple of
// always-available POSIX utilities. The tests intentionally lean on
// `true`, `false`, and `printf` so they run anywhere a developer
// is likely to clone the repo.
func TestCommand(t *testing.T) {
	t.Parallel()

	t.Run("Run streams stdout and stderr into the supplied writers", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		err := Command{}.Run(t.Context(),
			Options{Stdout: &stdout, Stderr: &stderr},
			"sh", "-c", `printf "hi"; printf "err" >&2`)
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if got := stdout.String(); got != "hi" {
			t.Fatalf("stdout = %q, want %q", got, "hi")
		}
		if got := stderr.String(); got != "err" {
			t.Fatalf("stderr = %q, want %q", got, "err")
		}
	})

	t.Run("Run propagates non-zero exit as an error", func(t *testing.T) {
		t.Parallel()
		err := Command{}.Run(t.Context(), Options{}, "false")
		if err == nil {
			t.Fatalf("Run returned nil for `false`, want exit error")
		}
	})

	t.Run("Run honours Dir for the subprocess working directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		var stdout bytes.Buffer
		err := Command{}.Run(t.Context(),
			Options{Dir: dir, Stdout: &stdout},
			"pwd")
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		// macOS resolves /var temp dirs through /private; tolerate
		// either form.
		got := strings.TrimSpace(stdout.String())
		if !strings.HasSuffix(got, dir) {
			t.Fatalf("pwd output = %q, want it to end with %q", got, dir)
		}
	})

	t.Run("LookPath surfaces ErrNotFound for missing binaries", func(t *testing.T) {
		t.Parallel()
		_, err := Command{}.LookPath("definitely-not-a-real-binary-name-xyzzy")
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("LookPath err = %v, want ErrNotFound", err)
		}
	})
}

// TestRunAllowNoPackages pins the soft-skip contract: a failure
// carrying the canonical "matched no packages" stderr signal is
// downgraded to a clean notice; real failures and successes pass
// through unchanged.
func TestRunAllowNoPackages(t *testing.T) {
	t.Parallel()

	t.Run("`matched no packages` (go toolchain) is demoted to a notice", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			stderrOut: `go: warning: "./..." matched no packages` + "\nno packages to vet\n",
			err:       errors.New("exit status 1"),
		}
		var stderr, notice bytes.Buffer
		err := RunAllowNoPackages(t.Context(), runner,
			Options{Stderr: &stderr}, &notice, "tests", "go", "vet", "./...")
		if err != nil {
			t.Fatalf("err = %v, want nil (signal must demote to notice)", err)
		}
		if !strings.Contains(notice.String(), "[tests]") {
			t.Fatalf("notice = %q, want it to name the module", notice.String())
		}
		if !strings.Contains(notice.String(), "no packages match") {
			t.Fatalf("notice = %q, want the skip message", notice.String())
		}
	})

	t.Run("`no go files to analyze` (golangci-lint) is demoted to a notice", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			stderrOut: `level=error msg="Running error: context loading failed: no go files to analyze"`,
			err:       errors.New("exit status 5"),
		}
		var notice bytes.Buffer
		err := RunAllowNoPackages(t.Context(), runner,
			Options{Stderr: io.Discard}, &notice, "tests", "golangci-lint", "run", "./...")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !strings.Contains(notice.String(), "[tests]") {
			t.Fatalf("notice = %q, want skip line", notice.String())
		}
	})

	t.Run("real failures pass through unchanged", func(t *testing.T) {
		t.Parallel()
		want := errors.New("vet found issues")
		runner := &fakeRunner{
			stderrOut: "real error not the no-packages one\n",
			err:       want,
		}
		err := RunAllowNoPackages(t.Context(), runner,
			Options{Stderr: io.Discard}, io.Discard, "core", "go", "vet", "./...")
		if !errors.Is(err, want) {
			t.Fatalf("err = %v, want propagation of the underlying error", err)
		}
	})

	t.Run("successful runs return nil with no notice", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		var notice bytes.Buffer
		err := RunAllowNoPackages(t.Context(), runner,
			Options{Stderr: io.Discard}, &notice, "core", "go", "vet", "./...")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if notice.Len() != 0 {
			t.Fatalf("notice = %q, want empty on success", notice.String())
		}
	})

	t.Run("real stderr still reaches the caller's writer on the demote path", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			stderrOut: `go: warning: "./..." matched no packages` + "\n",
			err:       errors.New("exit status 1"),
		}
		var stderr bytes.Buffer
		if err := RunAllowNoPackages(t.Context(), runner,
			Options{Stderr: &stderr}, io.Discard, "tests", "go", "vet", "./..."); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !strings.Contains(stderr.String(), "matched no packages") {
			t.Fatalf("stderr not tee'd into caller; got %q", stderr.String())
		}
	})

	t.Run("nil opts.Stderr is tolerated", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			stderrOut: `go: warning: "./..." matched no packages` + "\n",
			err:       errors.New("exit status 1"),
		}
		var notice bytes.Buffer
		if err := RunAllowNoPackages(t.Context(), runner,
			Options{}, &notice, "tests", "go", "vet", "./..."); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !strings.Contains(notice.String(), "[tests]") {
			t.Fatalf("notice = %q, want skip line", notice.String())
		}
	})
}

// fakeRunner satisfies [Runner] for the [RunAllowNoPackages]
// tests. It writes stderrOut to opts.Stderr (when non-nil) and
// returns err verbatim.
type fakeRunner struct {
	stderrOut string
	err       error
}

func (f *fakeRunner) Run(_ context.Context, opts Options, _ string, _ ...string) error {
	if opts.Stderr != nil && f.stderrOut != "" {
		_, _ = opts.Stderr.Write([]byte(f.stderrOut))
	}
	return f.err
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}
