// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package mod

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/modules"
)

// TestInstall pins the per-module `go mod download` followed by
// `go mod verify` shape `ergon mod install` runs.
func TestInstall(t *testing.T) {
	t.Run("runs download and verify per module in order", func(t *testing.T) {
		rec, restore := stubExec(t, nil)
		defer restore()

		err := Install(t.Context(), io.Discard, io.Discard, "/repo", []modules.Module{
			{Dir: "."},
			{Dir: "cli"},
		})
		if err != nil {
			t.Fatalf("Install err: %v", err)
		}
		want := []string{
			"/repo: go mod download",
			"/repo: go mod verify",
			"/repo/cli: go mod download",
			"/repo/cli: go mod verify",
		}
		if !equal(rec.calls, want) {
			t.Fatalf("calls = %+v, want %+v", rec.calls, want)
		}
	})

	t.Run("download failure aborts the run and surfaces the module dir", func(t *testing.T) {
		_, restore := stubExec(t, errors.New("network down"))
		defer restore()

		err := Install(t.Context(), io.Discard, io.Discard, "/repo", []modules.Module{{Dir: "cli"}})
		if err == nil {
			t.Fatal("Install returned nil, want error")
		}
		if !strings.Contains(err.Error(), "[cli]") {
			t.Fatalf("err = %v, want it to mention [cli]", err)
		}
	})
}

// TestTidy pins the per-module `go mod tidy` invocation pattern.
func TestTidy(t *testing.T) {
	t.Run("runs tidy per module in declared order", func(t *testing.T) {
		rec, restore := stubExec(t, nil)
		defer restore()

		err := Tidy(t.Context(), io.Discard, io.Discard, "/repo", []modules.Module{
			{Dir: "."},
			{Dir: "cli"},
			{Dir: "frontend/golang"},
		})
		if err != nil {
			t.Fatalf("Tidy err: %v", err)
		}
		want := []string{
			"/repo: go mod tidy",
			"/repo/cli: go mod tidy",
			"/repo/frontend/golang: go mod tidy",
		}
		if !equal(rec.calls, want) {
			t.Fatalf("calls = %+v, want %+v", rec.calls, want)
		}
	})
}

// TestVerify pins the contract that Verify runs tidy then a
// per-module `git diff --quiet`, surfaces dirty modules through
// [ErrDirty], and reports a clean result when every diff is silent.
func TestVerify(t *testing.T) {
	t.Run("clean diffs across every module return nil", func(t *testing.T) {
		_, restore := stubExec(t, nil)
		defer restore()

		err := Verify(t.Context(), io.Discard, io.Discard, "/repo", []modules.Module{
			{Dir: "."},
			{Dir: "cli"},
		})
		if err != nil {
			t.Fatalf("Verify err: %v", err)
		}
	})

	t.Run("dirty diff in one module surfaces ErrDirty with the module dir", func(t *testing.T) {
		_, restore := stubExecRouted(t, func(name string, args []string) error {
			if name == "git" && len(args) > 0 && args[0] == "diff" {
				// First module (.) is clean, "cli" has a dirty go.mod.
				for _, a := range args {
					if strings.HasPrefix(a, "cli/") {
						return errors.New("exit status 1")
					}
				}
				return nil
			}
			return nil
		})
		defer restore()

		err := Verify(t.Context(), io.Discard, io.Discard, "/repo", []modules.Module{
			{Dir: "."},
			{Dir: "cli"},
		})
		if !errors.Is(err, ErrDirty) {
			t.Fatalf("Verify err = %v, want wrapped ErrDirty", err)
		}
		if !strings.Contains(err.Error(), "cli") {
			t.Fatalf("err = %v, want it to mention `cli`", err)
		}
	})

	t.Run("tidy failure short-circuits the verify", func(t *testing.T) {
		_, restore := stubExecRouted(t, func(name string, args []string) error {
			if name == "go" && len(args) > 0 && args[0] == "mod" {
				return errors.New("tidy failed")
			}
			return nil
		})
		defer restore()

		err := Verify(t.Context(), io.Discard, io.Discard, "/repo", []modules.Module{{Dir: "."}})
		if errors.Is(err, ErrDirty) {
			t.Fatalf("Verify err = %v, want a tidy failure, not ErrDirty", err)
		}
		if err == nil {
			t.Fatalf("Verify returned nil, want tidy failure")
		}
	})
}

// recorder captures every subprocess invocation the test triggered.
// One entry per call, formatted as `<cwd>: <name> <args...>` so
// tests can assert sequence and working directory in one comparison.
type recorder struct {
	calls []string
}

// stubExec installs a runCmd that records every invocation and
// returns retErr (typically nil). The returned restore function
// must be called via defer to undo the patch.
func stubExec(t *testing.T, retErr error) (*recorder, func()) {
	t.Helper()
	rec := &recorder{}
	orig := runCmd
	runCmd = func(_ context.Context, cwd string, _, _ io.Writer, name string, args ...string) error {
		rec.calls = append(rec.calls, cwd+": "+name+" "+strings.Join(args, " "))
		return retErr
	}
	return rec, func() { runCmd = orig }
}

// stubExecRouted is like [stubExec] but routes the return value
// through a per-call decision function so tests can simulate a
// subset of subprocesses failing.
func stubExecRouted(t *testing.T, decide func(name string, args []string) error) (*recorder, func()) {
	t.Helper()
	rec := &recorder{}
	orig := runCmd
	runCmd = func(_ context.Context, cwd string, _, _ io.Writer, name string, args ...string) error {
		rec.calls = append(rec.calls, cwd+": "+name+" "+strings.Join(args, " "))
		return decide(name, args)
	}
	return rec, func() { runCmd = orig }
}

// equal reports whether two string slices are identical. Avoids
// the slices import for a one-call site.
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
