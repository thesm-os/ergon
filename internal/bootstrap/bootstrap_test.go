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

	t.Run("pinned versions override DefaultTools", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(simulatePresent("markdownlint-cli2"))

		cfg := Config{Pinned: map[string]string{
			DefaultTools[0].Pkg: "v1.2.3",
			DefaultTools[1].Pkg: "v9.9.9",
		}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if got := runner.commands[0]; got != "go install "+DefaultTools[0].Pkg+"@v1.2.3" {
			t.Errorf("commands[0] = %q, want pinned v1.2.3", got)
		}
		if got := runner.commands[1]; got != "go install "+DefaultTools[1].Pkg+"@v9.9.9" {
			t.Errorf("commands[1] = %q, want pinned v9.9.9", got)
		}
		// Unpinned defaults stay at @latest.
		if got := runner.commands[2]; got != "go install "+DefaultTools[2].Pkg+"@latest" {
			t.Errorf("commands[2] = %q, want @latest", got)
		}
	})

	t.Run("pinned versions override ExtraTools", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(simulatePresent("markdownlint-cli2"))

		cfg := Config{
			ExtraTools: []ToolSpec{{Pkg: "example.com/tool", Version: "v0.0.1"}},
			Pinned:     map[string]string{"example.com/tool": "v2.0.0"},
		}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		last := runner.commands[len(runner.commands)-1]
		if last != "go install example.com/tool@v2.0.0" {
			t.Errorf("last cmd = %q, want example.com/tool@v2.0.0 (Pinned beats spec.Version)", last)
		}
	})

	t.Run("pinned entry for unknown package is silently ignored", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(simulatePresent("markdownlint-cli2"))

		cfg := Config{Pinned: map[string]string{"example.com/not-installed": "v1.0.0"}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		for _, c := range runner.commands {
			if strings.Contains(c, "example.com/not-installed") {
				t.Fatalf("unexpected install for unknown pinned package: %q", c)
			}
		}
	})

	t.Run("empty pinned value falls back to the spec version", func(t *testing.T) {
		t.Parallel()
		runner := newFakeRunner(simulatePresent("markdownlint-cli2"))

		cfg := Config{Pinned: map[string]string{DefaultTools[0].Pkg: ""}}
		if err := Run(t.Context(), runner, io.Discard, io.Discard, cfg); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if got := runner.commands[0]; got != "go install "+DefaultTools[0].Pkg+"@latest" {
			t.Errorf("commands[0] = %q, want @latest (empty pin ignored)", got)
		}
	})
}

// TestResolveTools pins the pure merge function used by [Run]:
// declaration order is preserved across DefaultTools then
// ExtraTools, Pinned overrides win over any per-spec Version, and
// pinning an unknown package is a silent no-op.
func TestResolveTools(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  Config
		// checkFn receives the resolved tool list and returns an
		// error message when the contract under test is violated.
		check func([]ToolSpec) string
	}{
		{
			name: "empty config yields DefaultTools at latest",
			cfg:  Config{},
			check: func(got []ToolSpec) string {
				if len(got) != len(DefaultTools) {
					return "length mismatch"
				}
				for i, t := range got {
					if t.Pkg != DefaultTools[i].Pkg || t.Version != "latest" {
						return "default[i] mismatch"
					}
				}
				return ""
			},
		},
		{
			name: "ExtraTools append after DefaultTools",
			cfg: Config{ExtraTools: []ToolSpec{
				{Pkg: "example.com/x", Version: "v0.0.1"},
			}},
			check: func(got []ToolSpec) string {
				if len(got) != len(DefaultTools)+1 {
					return "length mismatch"
				}
				if got[len(got)-1].Pkg != "example.com/x" {
					return "extra not at tail"
				}
				return ""
			},
		},
		{
			name: "Pinned overrides DefaultTools",
			cfg:  Config{Pinned: map[string]string{DefaultTools[0].Pkg: "v1.0.0"}},
			check: func(got []ToolSpec) string {
				if got[0].Version != "v1.0.0" {
					return "default not overridden"
				}
				return ""
			},
		},
		{
			name: "Pinned overrides ExtraTools spec.Version",
			cfg: Config{
				ExtraTools: []ToolSpec{{Pkg: "example.com/x", Version: "v0.0.1"}},
				Pinned:     map[string]string{"example.com/x": "v9.9.9"},
			},
			check: func(got []ToolSpec) string {
				if got[len(got)-1].Version != "v9.9.9" {
					return "extra not overridden"
				}
				return ""
			},
		},
		{
			name: "Pinned for unknown package is a no-op",
			cfg:  Config{Pinned: map[string]string{"example.com/nope": "v1.0.0"}},
			check: func(got []ToolSpec) string {
				if len(got) != len(DefaultTools) {
					return "length changed"
				}
				return ""
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if msg := tc.check(resolveTools(tc.cfg)); msg != "" {
				t.Fatalf("%s: %s", tc.name, msg)
			}
		})
	}
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
