// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package markdown

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// TestFormat pins the contract of [Format]: invokes
// markdownlint-cli2 with `--fix` and the configured globs, swallows
// run-time errors as warnings, and skips with a hint when the
// binary is not on PATH.
func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("invokes markdownlint-cli2 with --fix and the globs", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Format(t.Context(), runner, io.Discard, io.Discard, "/repo", Defaults())
		if err != nil {
			t.Fatalf("Format err: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(runner.calls))
		}
		want := slices.Concat([]string{"--fix"}, Defaults().Globs)
		if !slices.Equal(runner.calls[0].args, want) {
			t.Fatalf("args = %+v, want %+v", runner.calls[0].args, want)
		}
	})

	t.Run("non-zero exit becomes a stderr warning, not an error", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{decide: func(string, []string) error {
			return errors.New("md errors")
		}}

		var stderr bytes.Buffer
		err := Format(t.Context(), runner, io.Discard, &stderr, "/repo", Defaults())
		if err != nil {
			t.Fatalf("Format err: %v, want nil", err)
		}
		if !strings.Contains(stderr.String(), "warning: markdownlint") {
			t.Fatalf("stderr = %q, want warning", stderr.String())
		}
	})

	t.Run("missing markdownlint-cli2 surfaces a hint and skips", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{lookPath: func(string) (string, error) {
			return "", exec.ErrNotFound
		}}

		var stderr bytes.Buffer
		err := Format(t.Context(), runner, io.Discard, &stderr, "/repo", Defaults())
		if err != nil {
			t.Fatalf("Format err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %+v, want zero", runner.calls)
		}
		if !strings.Contains(stderr.String(), "not on PATH") {
			t.Fatalf("stderr = %q, want hint", stderr.String())
		}
		if !strings.Contains(stderr.String(), "ergon bootstrap") {
			t.Fatalf("stderr = %q, want bootstrap hint", stderr.String())
		}
	})

	t.Run("empty globs is a no-op", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Format(t.Context(), runner, io.Discard, io.Discard, "/repo", Config{})
		if err != nil {
			t.Fatalf("Format err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %+v, want zero", runner.calls)
		}
	})
}

// TestLint pins the contract of [Lint]: invokes markdownlint-cli2
// without `--fix`, propagates run-time errors, and skips with a
// hint when the binary is not on PATH.
func TestLint(t *testing.T) {
	t.Parallel()

	t.Run("invokes markdownlint-cli2 with globs and no --fix", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Lint(t.Context(), runner, io.Discard, io.Discard, "/repo", Defaults())
		if err != nil {
			t.Fatalf("Lint err: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(runner.calls))
		}
		if slices.Contains(runner.calls[0].args, "--fix") {
			t.Fatalf("args = %+v, want no --fix", runner.calls[0].args)
		}
		if !slices.Equal(runner.calls[0].args, Defaults().Globs) {
			t.Fatalf("args = %+v, want %+v", runner.calls[0].args, Defaults().Globs)
		}
	})

	t.Run("non-zero exit propagates as an error", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{decide: func(string, []string) error {
			return errors.New("md errors")
		}}

		err := Lint(t.Context(), runner, io.Discard, io.Discard, "/repo", Defaults())
		if err == nil {
			t.Fatal("Lint returned nil, want error")
		}
	})

	t.Run("missing markdownlint-cli2 surfaces a hint and skips", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{lookPath: func(string) (string, error) {
			return "", exec.ErrNotFound
		}}

		var stderr bytes.Buffer
		err := Lint(t.Context(), runner, io.Discard, &stderr, "/repo", Defaults())
		if err != nil {
			t.Fatalf("Lint err: %v", err)
		}
		if !strings.Contains(stderr.String(), "ergon bootstrap") {
			t.Fatalf("stderr = %q, want bootstrap hint", stderr.String())
		}
	})

	t.Run("empty globs is a no-op", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{}

		err := Lint(t.Context(), runner, io.Discard, io.Discard, "/repo", Config{})
		if err != nil {
			t.Fatalf("Lint err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %+v, want zero", runner.calls)
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests.
type fakeRunner struct {
	calls    []recordedCall
	decide   func(name string, args []string) error
	lookPath func(name string) (string, error)
}

type recordedCall struct {
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, _ xexec.Options, name string, args ...string) error {
	f.calls = append(f.calls, recordedCall{name: name, args: slices.Clone(args)})
	if f.decide != nil {
		return f.decide(name, args)
	}
	return nil
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.lookPath != nil {
		return f.lookPath(name)
	}
	return "/usr/local/bin/" + name, nil
}
