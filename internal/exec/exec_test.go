// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package exec

import (
	"bytes"
	"errors"
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
