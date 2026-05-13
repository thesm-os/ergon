// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// TestLastTag pins the `git tag --list <prefix>v[0-9]*
// --sort=-v:refname` shape the resolver leans on and the empty-
// output → "" mapping (no prior tag exists).
func TestLastTag(t *testing.T) {
	t.Parallel()

	t.Run("returns the first line of git output", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{output: "v1.2.3\nv1.2.2\n"}
		got, err := LastTag(t.Context(), runner, "/repo", "")
		if err != nil {
			t.Fatalf("LastTag err: %v", err)
		}
		if got != "v1.2.3" {
			t.Fatalf("LastTag = %q, want v1.2.3", got)
		}
		if !slices.Contains(runner.calls[0].args, "--sort=-v:refname") {
			t.Fatalf("calls = %+v, want --sort=-v:refname", runner.calls[0].args)
		}
	})

	t.Run("submodule prefix is woven into the tag pattern", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{output: "foo/v0.0.1\n"}
		_, err := LastTag(t.Context(), runner, "/repo", "foo/")
		if err != nil {
			t.Fatalf("LastTag err: %v", err)
		}
		if !slices.Contains(runner.calls[0].args, "foo/v[0-9]*") {
			t.Fatalf("args = %+v, want foo/v[0-9]* pattern", runner.calls[0].args)
		}
	})

	t.Run("empty git output yields empty tag", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{output: ""}
		got, err := LastTag(t.Context(), runner, "/repo", "")
		if err != nil {
			t.Fatalf("LastTag err: %v", err)
		}
		if got != "" {
			t.Fatalf("LastTag = %q, want empty", got)
		}
	})

	t.Run("git error surfaces wrapped", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{runErr: errors.New("git missing")}
		_, err := LastTag(t.Context(), runner, "/repo", "")
		if err == nil {
			t.Fatal("LastTag err = nil, want non-nil")
		}
	})
}

// TestScopedCommits pins the `git log` arg construction (sinceTag
// elision when empty, include + `:(exclude)` pathspecs) and the
// parsed [CommitInfo] result.
func TestScopedCommits(t *testing.T) {
	t.Parallel()

	t.Run("range and exclude paths land in the git args", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{output: "feat: x\n\n" + commitTrailerSentinel + "\n"}
		_, err := ScopedCommits(t.Context(), runner, "/repo", "v1.0.0",
			[]string{"."}, []string{"cli"})
		if err != nil {
			t.Fatalf("ScopedCommits err: %v", err)
		}
		args := runner.calls[0].args
		if !slices.Contains(args, "v1.0.0..HEAD") {
			t.Fatalf("args = %+v, want v1.0.0..HEAD", args)
		}
		if !slices.Contains(args, ":(exclude)cli") {
			t.Fatalf("args = %+v, want :(exclude)cli", args)
		}
	})

	t.Run("empty sinceTag drops the range arg", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{}
		_, err := ScopedCommits(t.Context(), runner, "/repo", "",
			[]string{"."}, nil)
		if err != nil {
			t.Fatalf("ScopedCommits err: %v", err)
		}
		for _, a := range runner.calls[0].args {
			if strings.Contains(a, "..HEAD") {
				t.Fatalf("args = %+v, want no ..HEAD entry", runner.calls[0].args)
			}
		}
	})

	t.Run("commits parse out of the canonical log format", func(t *testing.T) {
		t.Parallel()
		raw := "feat: add x\n\nBREAKING CHANGE: removes y\n" + commitTrailerSentinel +
			"\nfix: y\n\n" + commitTrailerSentinel + "\n"
		runner := &gitFakeRunner{output: raw}
		commits, err := ScopedCommits(t.Context(), runner, "/repo", "",
			[]string{"."}, nil)
		if err != nil {
			t.Fatalf("ScopedCommits err: %v", err)
		}
		if len(commits) != 2 {
			t.Fatalf("commits = %d, want 2", len(commits))
		}
		if commits[0].Subject != "feat: add x" {
			t.Errorf("commits[0].Subject = %q", commits[0].Subject)
		}
		if !strings.Contains(commits[0].Body, "BREAKING CHANGE") {
			t.Errorf("commits[0].Body = %q", commits[0].Body)
		}
		if commits[1].Subject != "fix: y" {
			t.Errorf("commits[1].Subject = %q", commits[1].Subject)
		}
	})
}

// TestParseScopedCommits covers the empty + multi-record + body-
// less branches directly so the parser is exercised without a
// runner fixture.
func TestParseScopedCommits(t *testing.T) {
	t.Parallel()

	t.Run("empty input yields nil", func(t *testing.T) {
		t.Parallel()
		got := parseScopedCommits("")
		if len(got) != 0 {
			t.Fatalf("got = %+v, want nil", got)
		}
	})

	t.Run("subject-only commit has empty body", func(t *testing.T) {
		t.Parallel()
		got := parseScopedCommits("subject only\n" + commitTrailerSentinel + "\n")
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].Subject != "subject only" || got[0].Body != "" {
			t.Fatalf("commit = %+v", got[0])
		}
	})
}

// TestTag pins the `git tag -a -m <body> <name>` shape.
func TestTag(t *testing.T) {
	t.Parallel()

	runner := &gitFakeRunner{}
	if err := Tag(t.Context(), runner, "/repo", "v1.0.0", "body"); err != nil {
		t.Fatalf("Tag err: %v", err)
	}
	args := runner.calls[0].args
	want := []string{"tag", "-a", "-m", "body", "v1.0.0"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %+v, want %+v", args, want)
	}
}

// TestRunGit pins the failure path: the wrapped error mentions
// both the git subcommand and the captured stderr.
func TestRunGit(t *testing.T) {
	t.Parallel()

	t.Run("success returns captured output", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{output: "ok\n"}
		got, err := runGit(t.Context(), runner, "/repo", "status")
		if err != nil {
			t.Fatalf("runGit err: %v", err)
		}
		if got != "ok\n" {
			t.Fatalf("got = %q, want ok", got)
		}
	})

	t.Run("failure wraps the captured stderr", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{output: "fatal: not a repo", runErr: errors.New("exit 128")}
		_, err := runGit(t.Context(), runner, "/repo", "status")
		if err == nil {
			t.Fatal("runGit err = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "fatal: not a repo") {
			t.Fatalf("err = %v, want it to mention captured output", err)
		}
		if !strings.Contains(err.Error(), "status") {
			t.Fatalf("err = %v, want it to mention subcommand", err)
		}
	})
}

// gitFakeRunner is a concurrency-safe [xexec.Runner] used across
// the release tests. Every Run call records the arguments and
// writes `output` to opts.Stdout so [runGit] can return it.
type gitFakeRunner struct {
	mu     sync.Mutex
	calls  []gitCall
	output string
	runErr error
}

type gitCall struct {
	name string
	args []string
}

func (f *gitFakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.mu.Lock()
	f.calls = append(f.calls, gitCall{name: name, args: append([]string(nil), args...)})
	f.mu.Unlock()
	if opts.Stdout != nil && f.output != "" {
		_, _ = io.WriteString(opts.Stdout, f.output)
	}
	return f.runErr
}

func (*gitFakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}
