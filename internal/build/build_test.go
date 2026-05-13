// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package build

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// TestRun pins the per-module `go build ./...` invocation shape
// and the failure-aggregation behaviour [stage.PerModule]
// provides.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("invokes `go build ./...` per module", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}
		err := Run(t.Context(), runner, io.Discard, io.Discard, "/repo",
			[]modules.Module{{Dir: "."}, {Dir: "cli"}}, stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(runner.calls))
		}
		want := []string{
			filepath.ToSlash("/repo") + ": go build ./...",
			filepath.ToSlash("/repo/cli") + ": go build ./...",
		}
		got := slices.Clone(runner.calls)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("calls = %+v, want (set-equal) %+v", got, want)
		}
	})

	t.Run("default mode aggregates every per-module failure", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("compile error")}
		err := Run(t.Context(), runner, io.Discard, io.Discard, "/repo",
			[]modules.Module{{Dir: "."}, {Dir: "cli"}}, stage.Options{})
		if err == nil {
			t.Fatal("Run err = nil, want aggregated error")
		}
		if !strings.Contains(err.Error(), "[.]") || !strings.Contains(err.Error(), "[cli]") {
			t.Fatalf("err = %v, want it to mention every failing module", err)
		}
	})

	t.Run("fast mode short-circuits at the first failure", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("compile error")}
		err := Run(t.Context(), runner, io.Discard, io.Discard, "/repo",
			[]modules.Module{{Dir: "."}, {Dir: "cli"}}, stage.Options{Fast: true})
		if err == nil {
			t.Fatal("Run err = nil, want error")
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1 (short-circuit)", len(runner.calls))
		}
	})
}

// fakeRunner is concurrent-safe: stage.PerModule's default mode
// fans out per-module work into one goroutine each.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []string
	runErr error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.mu.Lock()
	f.calls = append(f.calls, filepath.ToSlash(opts.Dir)+": "+name+" "+strings.Join(args, " "))
	f.mu.Unlock()
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}
