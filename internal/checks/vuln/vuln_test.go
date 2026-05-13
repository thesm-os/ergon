// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package vuln

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// TestRun pins the per-module `govulncheck ./...` invocation shape
// and the first-failure short-circuit behaviour.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("runs govulncheck per module in order", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Run(t.Context(), runner, io.Discard, io.Discard, "/repo",
			[]modules.Module{{Dir: "."}, {Dir: "cli"}})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		want := []string{"/repo: govulncheck ./...", "/repo/cli: govulncheck ./..."}
		if !slices.Equal(runner.calls, want) {
			t.Fatalf("calls = %+v, want %+v", runner.calls, want)
		}
	})

	t.Run("first failure short-circuits and names the module", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("reachable vuln")}

		err := Run(t.Context(), runner, io.Discard, io.Discard, "/repo",
			[]modules.Module{{Dir: "cli"}, {Dir: "later"}})
		if err == nil {
			t.Fatal("Run returned nil, want error")
		}
		if !strings.Contains(err.Error(), "[cli]") {
			t.Fatalf("err = %v, want it to mention [cli]", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1 (short-circuit)", len(runner.calls))
		}
	})
}

type fakeRunner struct {
	calls  []string
	runErr error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.calls = append(f.calls, opts.Dir+": "+name+" "+strings.Join(args, " "))
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}
