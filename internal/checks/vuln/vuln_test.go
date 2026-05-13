// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package vuln

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/stage"
)

// TestRun pins the per-module `govulncheck ./...` invocation shape
// and the first-failure short-circuit behaviour.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("runs govulncheck per module in order", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Run(t.Context(), runner, io.Discard, io.Discard, "/repo",
			[]modules.Module{{Dir: "."}, {Dir: "cli"}}, stage.Options{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		want := []string{"/repo: govulncheck ./...", "/repo/cli: govulncheck ./..."}
		gotSorted := slices.Clone(runner.calls)
		wantSorted := slices.Clone(want)
		slices.Sort(gotSorted)
		slices.Sort(wantSorted)
		if !slices.Equal(gotSorted, wantSorted) {
			t.Fatalf("calls = %+v, want (set-equal) %+v", gotSorted, wantSorted)
		}
	})

	t.Run("default mode runs every module and aggregates failures", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("reachable vuln")}

		err := Run(t.Context(), runner, io.Discard, io.Discard, "/repo",
			[]modules.Module{{Dir: "cli"}, {Dir: "later"}}, stage.Options{})
		if err == nil {
			t.Fatal("Run returned nil, want aggregated error")
		}
		if !strings.Contains(err.Error(), "[cli]") || !strings.Contains(err.Error(), "[later]") {
			t.Fatalf("err = %v, want it to mention both modules", err)
		}
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2 (default mode runs everything)", len(runner.calls))
		}
	})

	t.Run("fast mode short-circuits at the first failure", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("reachable vuln")}

		err := Run(t.Context(), runner, io.Discard, io.Discard, "/repo",
			[]modules.Module{{Dir: "cli"}, {Dir: "later"}}, stage.Options{Fast: true})
		if err == nil {
			t.Fatal("Run returned nil, want error")
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1 (short-circuit)", len(runner.calls))
		}
	})
}

// fakeRunner is concurrent-safe: stage.PerModule's default mode
// fans out per-module work into one goroutine each, so writes to
// calls go through mu.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []string
	runErr error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.mu.Lock()
	f.calls = append(f.calls, opts.Dir+": "+name+" "+strings.Join(args, " "))
	f.mu.Unlock()
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}
