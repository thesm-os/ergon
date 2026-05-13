// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package mod

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// TestInstall pins the per-module `go mod download` followed by
// `go mod verify` shape `ergon mod install` runs.
func TestInstall(t *testing.T) {
	t.Parallel()

	t.Run("runs download and verify per module in order", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Install(t.Context(), runner, io.Discard, io.Discard, "/repo", []modules.Module{
			{Dir: "."},
			{Dir: "cli"},
		}, false)
		if err != nil {
			t.Fatalf("Install err: %v", err)
		}
		want := []string{
			"/repo: go mod download",
			"/repo: go mod verify",
			"/repo/cli: go mod download",
			"/repo/cli: go mod verify",
		}
		assertCalls(t, runner.calls, want)
	})

	t.Run("download failure aborts the run and names the module", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("network down")}

		err := Install(t.Context(), runner, io.Discard, io.Discard, "/repo", []modules.Module{{Dir: "cli"}}, false)
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
	t.Parallel()

	t.Run("runs tidy per module in declared order", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Tidy(t.Context(), runner, io.Discard, io.Discard, "/repo", []modules.Module{
			{Dir: "."},
			{Dir: "cli"},
			{Dir: "frontend/golang"},
		}, false)
		if err != nil {
			t.Fatalf("Tidy err: %v", err)
		}
		want := []string{
			"/repo: go mod tidy",
			"/repo/cli: go mod tidy",
			"/repo/frontend/golang: go mod tidy",
		}
		assertCalls(t, runner.calls, want)
	})
}

// TestVerify pins the contract that Verify runs tidy then a
// per-module `git diff --quiet`, surfaces dirty modules through
// [ErrDirty], and reports a clean result when every diff is silent.
func TestVerify(t *testing.T) {
	t.Parallel()

	t.Run("clean diffs across every module return nil", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Verify(t.Context(), runner, io.Discard, io.Discard, "/repo", []modules.Module{
			{Dir: "."},
			{Dir: "cli"},
		}, false)
		if err != nil {
			t.Fatalf("Verify err: %v", err)
		}
	})

	t.Run("dirty diff in one module surfaces ErrDirty naming that module", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{decide: func(name string, args []string) error {
			if name != "git" || len(args) == 0 || args[0] != "diff" {
				return nil
			}
			for _, a := range args {
				if strings.HasPrefix(a, "cli/") {
					return errors.New("exit status 1")
				}
			}
			return nil
		}}

		err := Verify(t.Context(), runner, io.Discard, io.Discard, "/repo", []modules.Module{
			{Dir: "."},
			{Dir: "cli"},
		}, false)
		if !errors.Is(err, ErrDirty) {
			t.Fatalf("Verify err = %v, want wrapped ErrDirty", err)
		}
		if !strings.Contains(err.Error(), "cli") {
			t.Fatalf("err = %v, want it to mention `cli`", err)
		}
	})

	t.Run("tidy failure short-circuits the verify", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{decide: func(name string, args []string) error {
			if name == "go" && len(args) > 0 && args[0] == "mod" {
				return errors.New("tidy failed")
			}
			return nil
		}}

		err := Verify(t.Context(), runner, io.Discard, io.Discard, "/repo", []modules.Module{{Dir: "."}}, false)
		if errors.Is(err, ErrDirty) {
			t.Fatalf("Verify err = %v, want a tidy failure, not ErrDirty", err)
		}
		if err == nil {
			t.Fatalf("Verify returned nil, want tidy failure")
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. Each call is
// recorded as `<cwd>: <name> <args...>`; optional decide and
// runErr fields control the simulated return value.
type fakeRunner struct {
	calls  []string
	runErr error
	decide func(name string, args []string) error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.calls = append(f.calls, opts.Dir+": "+name+" "+strings.Join(args, " "))
	if f.decide != nil {
		return f.decide(name, args)
	}
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// assertCalls compares the recorded calls against want. Want must
// be exactly equal to got.
func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("calls[%d] = %q, want %q", i, got[i], w)
		}
	}
}
