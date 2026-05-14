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
// `perCallOutputs` lets a multi-call test (e.g. TagCommit's two
// `git tag -l` + `git rev-list` sequence) inject a separate
// stdout body per invocation; entries beyond the slice fall
// back to `output`. `decide` lets a test inject per-call error
// decisions when the same fake handles a mixed pass/fail
// sequence.
type gitFakeRunner struct {
	mu             sync.Mutex
	calls          []gitCall
	output         string
	perCallOutputs []string
	runErr         error
	decide         func(name string, args []string) error
}

type gitCall struct {
	name string
	args []string
}

func (f *gitFakeRunner) Run(_ context.Context, opts xexec.Options, name string, args ...string) error {
	f.mu.Lock()
	idx := len(f.calls)
	f.calls = append(f.calls, gitCall{name: name, args: append([]string(nil), args...)})
	body := f.output
	if idx < len(f.perCallOutputs) {
		body = f.perCallOutputs[idx]
	}
	decide := f.decide
	f.mu.Unlock()
	if opts.Stdout != nil && body != "" {
		_, _ = io.WriteString(opts.Stdout, body)
	}
	if decide != nil {
		return decide(name, args)
	}
	return f.runErr
}

func (*gitFakeRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// TestTagCommit pins the two-step lookup [TagCommit] performs:
// `git tag -l <name>` to test existence, then
// `git rev-list -n 1 refs/tags/<name>` to resolve the commit.
// Missing-tag and present-tag branches both surface.
func TestTagCommit(t *testing.T) {
	t.Parallel()

	t.Run("absent tag returns exists=false", func(t *testing.T) {
		t.Parallel()
		// `git tag -l <missing>` outputs nothing; rev-list is not called.
		runner := &gitFakeRunner{output: ""}
		commit, exists, err := TagCommit(t.Context(), runner, "/repo", "v1.0.0")
		if err != nil {
			t.Fatalf("TagCommit err: %v", err)
		}
		if exists {
			t.Errorf("exists = true, want false for missing tag")
		}
		if commit != "" {
			t.Errorf("commit = %q, want empty", commit)
		}
		// Only the existence probe ran; no rev-list call.
		if len(runner.calls) != 1 {
			t.Errorf("calls = %d, want 1 (existence probe only)", len(runner.calls))
		}
	})

	t.Run("present tag resolves to its commit SHA", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{perCallOutputs: []string{
			"v1.0.0\n", // git tag -l
			"abc123def4567890abcdef1234567890abcdef12\n", // git rev-list
		}}
		commit, exists, err := TagCommit(t.Context(), runner, "/repo", "v1.0.0")
		if err != nil {
			t.Fatalf("TagCommit err: %v", err)
		}
		if !exists {
			t.Errorf("exists = false, want true")
		}
		if commit != "abc123def4567890abcdef1234567890abcdef12" {
			t.Errorf("commit = %q, want full SHA", commit)
		}
	})
}

// TestHeadCommit pins the `git rev-parse HEAD` shape and the
// trim-trailing-newline output normalisation.
func TestHeadCommit(t *testing.T) {
	t.Parallel()

	runner := &gitFakeRunner{output: "deadbeef00000000\n"}
	got, err := HeadCommit(t.Context(), runner, "/repo")
	if err != nil {
		t.Fatalf("HeadCommit err: %v", err)
	}
	if got != "deadbeef00000000" {
		t.Errorf("HeadCommit = %q, want trimmed SHA", got)
	}
	if len(runner.calls) != 1 || !slices.Equal(runner.calls[0].args, []string{"rev-parse", "HEAD"}) {
		t.Errorf("calls = %+v, want one `rev-parse HEAD`", runner.calls)
	}
}

// TestEnsureTag pins the idempotency contract: absent tag is
// created; tag at HEAD is preserved (no-op); tag at a different
// commit surfaces an error so callers don't silently move a
// published tag.
func TestEnsureTag(t *testing.T) {
	t.Parallel()

	t.Run("absent tag is created", func(t *testing.T) {
		t.Parallel()
		// tag -l returns empty → tag does not exist → Tag is called.
		runner := &gitFakeRunner{output: ""}
		if err := EnsureTag(t.Context(), runner, "/repo", "v1.0.0", "body"); err != nil {
			t.Fatalf("EnsureTag err: %v", err)
		}
		// Expected calls: tag -l v1.0.0 ; tag -a -m body v1.0.0.
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2 (existence probe + tag creation)", len(runner.calls))
		}
		create := runner.calls[1].args
		want := []string{"tag", "-a", "-m", "body", "v1.0.0"}
		if !slices.Equal(create, want) {
			t.Errorf("create call = %+v, want %+v", create, want)
		}
	})

	t.Run("tag at HEAD is idempotent no-op", func(t *testing.T) {
		t.Parallel()
		sha := "abc123def4567890abcdef1234567890abcdef12"
		runner := &gitFakeRunner{perCallOutputs: []string{
			"v1.0.0\n", // tag -l
			sha + "\n", // rev-list
			sha + "\n", // rev-parse HEAD
		}}
		if err := EnsureTag(t.Context(), runner, "/repo", "v1.0.0", "body"); err != nil {
			t.Fatalf("EnsureTag err: %v", err)
		}
		// No tag -a call should have been made.
		for _, c := range runner.calls {
			if len(c.args) >= 2 && c.args[0] == "tag" && c.args[1] == "-a" {
				t.Errorf("unexpected tag-create call %+v on existing-tag path", c.args)
			}
		}
	})

	t.Run("tag at different commit surfaces an error", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{perCallOutputs: []string{
			"v1.0.0\n",               // tag -l
			"aaaaaaaaaaaa11111111\n", // rev-list (existing tag)
			"bbbbbbbbbbbb22222222\n", // rev-parse HEAD (different)
		}}
		err := EnsureTag(t.Context(), runner, "/repo", "v1.0.0", "body")
		if err == nil {
			t.Fatal("EnsureTag returned nil, want commit-mismatch error")
		}
		if !strings.Contains(err.Error(), "v1.0.0") {
			t.Errorf("err = %v, want it to mention the conflicting tag name", err)
		}
		if !strings.Contains(err.Error(), "aaaaaaaaaaaa") || !strings.Contains(err.Error(), "bbbbbbbbbbbb") {
			t.Errorf("err = %v, want it to mention both shortened SHAs", err)
		}
	})
}

// TestShortSHA covers both branches of the SHA truncator: a
// full-length git SHA is shortened to 12 chars; a shorter input
// (e.g. a synthetic SHA used in tests) is returned unchanged.
func TestShortSHA(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "full SHA truncates to 12", in: "abc123def4567890abcdef", want: "abc123def456"},
		{name: "exactly 12 chars stays put", in: "012345678901", want: "012345678901"},
		{name: "shorter input passes through unchanged", in: "short", want: "short"},
		{name: "empty input passes through unchanged", in: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shortSHA(tc.in); got != tc.want {
				t.Errorf("shortSHA(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPreflightTagSigning pins the create-then-delete probe
// shape and the failure paths (probe creation fails / cleanup
// fails).
func TestPreflightTagSigning(t *testing.T) {
	t.Parallel()

	t.Run("probe succeeds when both git operations return ok", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{}
		if err := PreflightTagSigning(t.Context(), runner, "/repo"); err != nil {
			t.Fatalf("PreflightTagSigning err: %v", err)
		}
		// Expected calls in order:
		//   1. tag -d <probe>   (defensive cleanup; may "fail" silently)
		//   2. tag -a -m <msg> <probe>
		//   3. tag -d <probe>   (real cleanup)
		if len(runner.calls) != 3 {
			t.Fatalf("calls = %d, want 3", len(runner.calls))
		}
		create := runner.calls[1].args
		if len(create) < 5 || create[0] != "tag" || create[1] != "-a" || create[4] != signingProbeName {
			t.Errorf("create call = %+v, want tag -a -m <msg> <probe>", create)
		}
	})

	t.Run("probe failure wraps the underlying git error with a hint", func(t *testing.T) {
		t.Parallel()
		runner := &gitFakeRunner{decide: func(_ string, args []string) error {
			// First call is the defensive `tag -d`; let it succeed.
			// Second call is the probe `tag -a`; fail it to simulate
			// a broken signing setup.
			if len(args) >= 2 && args[0] == "tag" && args[1] == "-a" {
				return errors.New("gpg: signing failed")
			}
			return nil
		}}
		err := PreflightTagSigning(t.Context(), runner, "/repo")
		if err == nil {
			t.Fatal("PreflightTagSigning returned nil, want signing error")
		}
		if !strings.Contains(err.Error(), "preflight failed") {
			t.Errorf("err = %v, want it to mention preflight failure", err)
		}
		if !strings.Contains(err.Error(), "GPG") {
			t.Errorf("err = %v, want it to mention GPG as the most common cause", err)
		}
	})
}
