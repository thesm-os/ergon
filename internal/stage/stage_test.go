// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package stage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// TestPerModule pins the contract: iterates every module under
// default mode and collects every failure; short-circuits at the
// first failure under fast mode; renders the section header +
// summary block around the calls.
func TestPerModule(t *testing.T) {
	t.Parallel()

	t.Run("default mode runs every module and aggregates failures", func(t *testing.T) {
		t.Parallel()
		var visited []string
		var stdout strings.Builder
		err := PerModule(t.Context(), &stdout,
			[]modules.Module{{Dir: "a"}, {Dir: "b"}, {Dir: "c"}}, false,
			"my-stage", "test details",
			func(_ context.Context, m modules.Module) (bool, error) {
				visited = append(visited, m.Dir)
				if m.Dir == "b" {
					return false, errors.New("middle fail")
				}
				return false, nil
			})
		if err == nil {
			t.Fatal("PerModule returned nil, want aggregated error")
		}
		if len(visited) != 3 {
			t.Fatalf("visited = %+v, want 3 modules (no short-circuit)", visited)
		}
		if !strings.Contains(err.Error(), "[b]") {
			t.Fatalf("err = %v, want it to mention [b]", err)
		}
	})

	t.Run("fast mode aborts at the first failure", func(t *testing.T) {
		t.Parallel()
		var visited []string
		var stdout strings.Builder
		err := PerModule(t.Context(), &stdout,
			[]modules.Module{{Dir: "a"}, {Dir: "b"}, {Dir: "c"}}, true,
			"my-stage", "test details",
			func(_ context.Context, m modules.Module) (bool, error) {
				visited = append(visited, m.Dir)
				if m.Dir == "b" {
					return false, errors.New("middle fail")
				}
				return false, nil
			})
		if err == nil {
			t.Fatal("PerModule returned nil, want error")
		}
		if len(visited) != 2 {
			t.Fatalf("visited = %+v, want 2 (fast aborts after b)", visited)
		}
	})

	t.Run("every-pass run returns nil and emits the pass summary", func(t *testing.T) {
		t.Parallel()
		var stdout strings.Builder
		err := PerModule(t.Context(), &stdout,
			[]modules.Module{{Dir: "."}}, false,
			"my-stage", "test details",
			func(_ context.Context, _ modules.Module) (bool, error) {
				return false, nil
			})
		if err != nil {
			t.Fatalf("PerModule err: %v", err)
		}
		if !strings.Contains(stdout.String(), "my-stage") {
			t.Fatalf("stdout missing header: %q", stdout.String())
		}
		if !strings.Contains(stdout.String(), "every module passed") {
			t.Fatalf("stdout missing pass message: %q", stdout.String())
		}
	})

	t.Run("skipped module never fails the stage", func(t *testing.T) {
		t.Parallel()
		var stdout strings.Builder
		err := PerModule(t.Context(), &stdout,
			[]modules.Module{{Dir: "tests"}}, false,
			"my-stage", "test details",
			func(_ context.Context, _ modules.Module) (bool, error) {
				return true, nil
			})
		if err != nil {
			t.Fatalf("PerModule err: %v, want nil for a pure skip", err)
		}
		if !strings.Contains(stdout.String(), "skipped (no packages match build tags)") {
			t.Fatalf("stdout missing skip note: %q", stdout.String())
		}
	})
}

// TestRunAllowSkip pins the wrapper: the underlying call's skip
// notice flips skipped=true and is also tee'd through to the
// notice writer; a real failure surfaces as a non-nil error with
// skipped=false.
func TestRunAllowSkip(t *testing.T) {
	t.Parallel()

	t.Run("`matched no packages` signal returns skipped=true", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			stderrOut: `go: warning: "./..." matched no packages` + "\n",
			err:       errors.New("exit status 1"),
		}
		var notice strings.Builder
		skipped, err := RunAllowSkip(t.Context(), runner,
			xexec.Options{Stderr: io.Discard},
			&notice, "tests", "go", "vet", "./...")
		if err != nil {
			t.Fatalf("err = %v, want nil after demotion", err)
		}
		if !skipped {
			t.Fatal("skipped = false, want true")
		}
		if !strings.Contains(notice.String(), "[tests]") {
			t.Fatalf("notice = %q, want skip line tee'd through", notice.String())
		}
	})

	t.Run("clean success returns skipped=false, err=nil", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		skipped, err := RunAllowSkip(t.Context(), runner,
			xexec.Options{Stderr: io.Discard}, io.Discard,
			"core", "go", "vet", "./...")
		if err != nil || skipped {
			t.Fatalf("got (skipped=%v, err=%v), want (false, nil)", skipped, err)
		}
	})

	t.Run("real failure surfaces unchanged with skipped=false", func(t *testing.T) {
		t.Parallel()
		want := errors.New("vet found issues")
		runner := &fakeRunner{
			stderrOut: "real error not the no-packages one\n",
			err:       want,
		}
		skipped, err := RunAllowSkip(t.Context(), runner,
			xexec.Options{Stderr: io.Discard}, io.Discard,
			"core", "go", "vet", "./...")
		if !errors.Is(err, want) {
			t.Fatalf("err = %v, want propagation", err)
		}
		if skipped {
			t.Fatal("skipped = true for a real failure")
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for the stage tests. It
// writes stderrOut to opts.Stderr when non-nil and returns err
// verbatim.
type fakeRunner struct {
	stderrOut string
	err       error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	if opts.Stderr != nil && f.stderrOut != "" {
		_, _ = opts.Stderr.Write([]byte(f.stderrOut))
	}
	return f.err
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}
