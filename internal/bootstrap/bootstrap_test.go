// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// TestDefaults pins the default [Config]: empty ExtraTools so the
// built-in [DefaultTools] list is the only thing [Run] installs
// out of the box.
func TestDefaults(t *testing.T) {
	t.Parallel()

	got := Defaults()
	if len(got.ExtraTools) != 0 {
		t.Fatalf("Defaults().ExtraTools = %+v, want empty", got.ExtraTools)
	}
}

// TestRun pins the contract of [Run]: every default tool gets a
// `go install` invocation, per-repo extras are appended after the
// defaults, a missing markdownlint-cli2 triggers an npm install
// when npm is available, and the absence of both surfaces as a
// warning rather than an error.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("installs every default tool in declared order", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(simulatePresent("markdownlint-cli2"))

		err := Run(t.Context(), runner, io.Discard, io.Discard, Config{})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		want := make([]string, len(DefaultTools))
		for i, tool := range DefaultTools {
			want[i] = "go install " + tool.Pkg + "@latest"
		}
		assertCommands(t, runner.commands, want)
	})

	t.Run("extras append after defaults in declared order", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(simulatePresent("markdownlint-cli2"))

		cfg := Config{ExtraTools: []ToolSpec{
			{Pkg: "example.com/tool-a", Version: "latest"},
			{Pkg: "example.com/tool-b", Version: "v1.2.3"},
		}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		got := runner.commands
		if len(got) != len(DefaultTools)+2 {
			t.Fatalf("recorded %d commands, want %d", len(got), len(DefaultTools)+2)
		}
		if got[len(DefaultTools)] != "go install example.com/tool-a@latest" {
			t.Fatalf("extras[0] = %q", got[len(DefaultTools)])
		}
		if got[len(DefaultTools)+1] != "go install example.com/tool-b@v1.2.3" {
			t.Fatalf("extras[1] = %q", got[len(DefaultTools)+1])
		}
	})

	t.Run("empty version on an extra defaults to latest", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(simulatePresent("markdownlint-cli2"))

		cfg := Config{ExtraTools: []ToolSpec{{Pkg: "example.com/tool"}}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		last := runner.commands[len(runner.commands)-1]
		if last != "go install example.com/tool@latest" {
			t.Fatalf("last cmd = %q, want go install example.com/tool@latest", last)
		}
	})

	t.Run("missing markdownlint with npm available triggers npm install", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(func(name string) error {
			if name == "markdownlint-cli2" {
				return exec.ErrNotFound
			}
			return nil // npm available
		})

		var stderr bytes.Buffer
		if err := Run(t.Context(), runner, io.Discard, &stderr, Config{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		last := runner.commands[len(runner.commands)-1]
		if last != "npm install -g markdownlint-cli2" {
			t.Fatalf("last cmd = %q, want npm install -g markdownlint-cli2", last)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr = %q, want empty", stderr.String())
		}
	})

	t.Run("missing markdownlint and missing npm surfaces a warning", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(func(_ string) error { return exec.ErrNotFound })

		var stderr bytes.Buffer
		if err := Run(t.Context(), runner, io.Discard, &stderr, Config{}); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		for _, c := range runner.commands {
			if strings.HasPrefix(c, "npm") {
				t.Fatalf("unexpected npm invocation: %q", c)
			}
		}
		if !strings.Contains(stderr.String(), "markdownlint-cli2 not installed") {
			t.Fatalf("stderr = %q, want warning about markdownlint", stderr.String())
		}
	})

	t.Run("install failure aborts the run", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(simulatePresent("markdownlint-cli2"))
		runner.runErr = errors.New("network down")

		err := Run(t.Context(), runner, io.Discard, io.Discard, Config{})
		if err == nil {
			t.Fatalf("Run returned nil, want error")
		}
		if !strings.Contains(err.Error(), DefaultTools[0].Pkg) {
			t.Fatalf("err = %v, want mention of first tool %q", err, DefaultTools[0].Pkg)
		}
	})
}

// fakeRunner satisfies [xexec.Runner] for tests. It records every
// Run invocation as a `<name> <args>` string and honours a
// presence policy from LookPath.
type fakeRunner struct {
	commands []string
	runErr   error
	present  func(name string) error
}

func newFakeRunner(present func(string) error) *fakeRunner {
	return &fakeRunner{present: present}
}

func (f *fakeRunner) Run(_ context.Context, _ xexec.Options, name string, args ...string) error {
	f.commands = append(f.commands, strings.Join(append([]string{name}, args...), " "))
	return f.runErr
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if err := f.present(name); err != nil {
		return "", err
	}
	return "/usr/local/bin/" + name, nil
}

// simulatePresent returns a presence policy that reports the named
// tool as installed and everything else as missing.
func simulatePresent(name string) func(string) error {
	return func(n string) error {
		if n == name {
			return nil
		}
		return exec.ErrNotFound
	}
}

// assertCommands fails the test when the recorder did not capture
// the expected command sequence (the want sequence must be a prefix
// of the recorded calls so callers can ignore trailing probes).
func assertCommands(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) < len(want) {
		t.Fatalf("recorded %d commands, want at least %d (%+v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("commands[%d] = %q, want %q", i, got[i], w)
		}
	}
}
