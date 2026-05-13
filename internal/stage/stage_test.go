// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package stage

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
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
		var (
			mu      sync.Mutex
			visited []string
		)
		var stdout strings.Builder
		err := PerModule(t.Context(), &stdout,
			[]modules.Module{{Dir: "a"}, {Dir: "b"}, {Dir: "c"}}, Options{},
			"my-stage", "test details",
			func(_ context.Context, m modules.Module) StepResult {
				mu.Lock()
				visited = append(visited, m.Dir)
				mu.Unlock()
				if m.Dir == "b" {
					return StepResult{Err: errors.New("middle fail")}
				}
				return StepResult{}
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
		// Fast mode is serial inside PerModule, so no mutex needed.
		var visited []string
		var stdout strings.Builder
		err := PerModule(t.Context(), &stdout,
			[]modules.Module{{Dir: "a"}, {Dir: "b"}, {Dir: "c"}}, Options{Fast: true},
			"my-stage", "test details",
			func(_ context.Context, m modules.Module) StepResult {
				visited = append(visited, m.Dir)
				if m.Dir == "b" {
					return StepResult{Err: errors.New("middle fail")}
				}
				return StepResult{}
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
			[]modules.Module{{Dir: "."}}, Options{},
			"my-stage", "test details",
			func(_ context.Context, _ modules.Module) StepResult {
				return StepResult{}
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
			[]modules.Module{{Dir: "tests"}}, Options{},
			"my-stage", "test details",
			func(_ context.Context, _ modules.Module) StepResult {
				return StepResult{Skipped: true}
			})
		if err != nil {
			t.Fatalf("PerModule err: %v, want nil for a pure skip", err)
		}
		if !strings.Contains(stdout.String(), "skipped (no packages match build tags)") {
			t.Fatalf("stdout missing skip note: %q", stdout.String())
		}
	})

	t.Run("default mode runs modules concurrently", func(t *testing.T) {
		t.Parallel()
		// Each fn waits on `ready` before returning. If execution
		// were serial, only one goroutine could be waiting at a
		// time and we'd deadlock; concurrent execution lets all N
		// goroutines reach the wait and then proceed.
		const n = 4
		ready := make(chan struct{})
		started := make(chan struct{}, n)
		var stdout strings.Builder
		mods := make([]modules.Module, n)
		for i := range n {
			mods[i] = modules.Module{Dir: string(rune('a' + i))}
		}
		done := make(chan error, 1)
		go func() {
			done <- PerModule(t.Context(), &stdout, mods, Options{},
				"my-stage", "test details",
				func(_ context.Context, _ modules.Module) StepResult {
					started <- struct{}{}
					<-ready
					return StepResult{}
				})
		}()
		// Wait until every fn has started, then release them.
		for range n {
			<-started
		}
		close(ready)
		if err := <-done; err != nil {
			t.Fatalf("PerModule err: %v", err)
		}
	})

	t.Run("captured tool output is rendered under the failing verdict", func(t *testing.T) {
		t.Parallel()
		var stdout strings.Builder
		_ = PerModule(t.Context(), &stdout,
			[]modules.Module{{Dir: "a"}}, Options{},
			"my-stage", "details",
			func(_ context.Context, _ modules.Module) StepResult {
				return StepResult{
					Err:    errors.New("boom"),
					Output: "line1\nline2\n",
				}
			})
		body := stdout.String()
		if !strings.Contains(body, "line1") || !strings.Contains(body, "line2") {
			t.Fatalf("stdout missing captured output: %q", body)
		}
	})
}

// TestRunAllowSkip pins the capture-vs-stream contract: the skip
// signal demotes failures regardless of mode; buffered mode keeps
// stdout silent and returns Output on failure; verbose mode
// streams output live and leaves Output empty.
func TestRunAllowSkip(t *testing.T) {
	t.Parallel()

	t.Run("buffered mode (no `matched no packages`) returns Output on failure", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{
			stdoutOut: "vet finding here\n",
			err:       errors.New("exit status 1"),
		}
		var notice strings.Builder
		r := RunAllowSkip(t.Context(), runner, Options{}, "/dir", "core",
			io.Discard, io.Discard, &notice, "go", "vet", "./...")
		if r.Err == nil {
			t.Fatal("err = nil, want failure")
		}
		if !strings.Contains(r.Output, "vet finding here") {
			t.Fatalf("Output = %q, want captured stdout", r.Output)
		}
		if r.Skipped {
			t.Fatal("Skipped = true for a real failure")
		}
	})

	t.Run("buffered mode drops captured output on success", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{stdoutOut: "everything OK"}
		r := RunAllowSkip(t.Context(), runner, Options{}, "/dir", "core",
			io.Discard, io.Discard, io.Discard, "go", "vet", "./...")
		if r.Err != nil || r.Output != "" || r.Skipped {
			t.Fatalf("got %+v, want zero StepResult", r)
		}
	})

	t.Run("`matched no packages` signal demotes failure to skip in both modes", func(t *testing.T) {
		t.Parallel()
		for _, opts := range []Options{{}, {Verbose: true}} {
			runner := &fakeRunner{
				stderrOut: `go: warning: "./..." matched no packages` + "\n",
				err:       errors.New("exit status 1"),
			}
			var notice strings.Builder
			r := RunAllowSkip(t.Context(), runner, opts, "/dir", "tests",
				io.Discard, io.Discard, &notice, "go", "vet", "./...")
			if r.Err != nil {
				t.Fatalf("verbose=%v: err = %v, want nil", opts.Verbose, r.Err)
			}
			if !r.Skipped {
				t.Fatalf("verbose=%v: Skipped = false, want true", opts.Verbose)
			}
			if !strings.Contains(notice.String(), "[tests]") {
				t.Fatalf("verbose=%v: notice = %q, want skip line", opts.Verbose, notice.String())
			}
		}
	})

	t.Run("verbose mode streams output and leaves Output empty", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{stdoutOut: "live output\n"}
		var liveOut strings.Builder
		r := RunAllowSkip(t.Context(), runner, Options{Verbose: true}, "/dir", "core",
			&liveOut, io.Discard, io.Discard, "go", "vet", "./...")
		if r.Err != nil {
			t.Fatalf("err = %v, want nil", r.Err)
		}
		if r.Output != "" {
			t.Fatalf("Output = %q, want empty in verbose mode", r.Output)
		}
		if !strings.Contains(liveOut.String(), "live output") {
			t.Fatalf("verbose stdout did not receive bytes: %q", liveOut.String())
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for the stage tests. It
// writes stdoutOut to opts.Stdout and stderrOut to opts.Stderr
// (each only when the destination is non-nil) and returns err
// verbatim.
type fakeRunner struct {
	stdoutOut string
	stderrOut string
	err       error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	if opts.Stdout != nil && f.stdoutOut != "" {
		_, _ = opts.Stdout.Write([]byte(f.stdoutOut))
	}
	if opts.Stderr != nil && f.stderrOut != "" {
		_, _ = opts.Stderr.Write([]byte(f.stderrOut))
	}
	return f.err
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}
