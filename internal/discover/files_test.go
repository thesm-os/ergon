// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package discover

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// TestGitFiles pins the parser of `git ls-files -z` output and
// the suffix filter.
func TestGitFiles(t *testing.T) {
	t.Parallel()

	t.Run("returns every file listed by git", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{output: nullSep("README.md", "cmd/ergon/main.go", "internal/config/config.go")}

		got, err := GitFiles(t.Context(), runner, "/repo", "")
		if err != nil {
			t.Fatalf("GitFiles err: %v", err)
		}
		want := []string{"README.md", "cmd/ergon/main.go", "internal/config/config.go"}
		if !slices.Equal(got, want) {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("suffix filter selects matching files only", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{output: nullSep("README.md", "cmd/ergon/main.go", "internal/config/config.go")}

		got, err := GitFiles(t.Context(), runner, "/repo", ".go")
		if err != nil {
			t.Fatalf("GitFiles err: %v", err)
		}
		want := []string{"cmd/ergon/main.go", "internal/config/config.go"}
		if !slices.Equal(got, want) {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("subprocess failure wraps the captured output", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{runErr: errors.New("fatal"), output: []byte("fatal: not a git repository")}

		_, err := GitFiles(t.Context(), runner, "/repo", "")
		if err == nil {
			t.Fatal("GitFiles returned nil, want error")
		}
		if !strings.Contains(err.Error(), "not a git repository") {
			t.Fatalf("err = %v, want captured output in message", err)
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. The output bytes
// are written verbatim to the caller's Stdout writer; runErr is
// returned from Run.
type fakeRunner struct {
	output []byte
	runErr error
}

func (f *fakeRunner) Run(_ context.Context, opts xexec.Options, _ string, _ ...string) error {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write(f.output)
	}
	return f.runErr
}

func (*fakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// nullSep joins paths with the NUL byte separator git uses with -z.
func nullSep(paths ...string) []byte {
	return []byte(strings.Join(paths, "\x00") + "\x00")
}
