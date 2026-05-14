// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.thesmos.sh/ergon/internal/bootstrap"
)

// TestResolvePath pins the two branches of the path resolver: an
// empty input falls back to the literal `.ergon.yaml` (relative to
// the current working directory) and an explicit input is honoured
// verbatim. The resolver replaces the viper-driven search of the
// previous loader; without viper there is no third "viper resolved
// the path" branch to exercise.
func TestResolvePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty input falls back to the literal default",
			input: "",
			want:  filepath.Join(".", ".ergon.yaml"),
		},
		{
			name:  "explicit path is honoured verbatim",
			input: "/custom.yaml",
			want:  "/custom.yaml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolvePath(tc.input); got != tc.want {
				t.Errorf("resolvePath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestLoad pins the loader's contract: defaults apply when no file
// is present, fields parsed from the file override the defaults,
// unknown fields surface as [ErrUnknownField], malformed YAML
// surfaces as a non-[ErrUnknownField] read error, and an
// empty / comments-only file is treated as "no overrides" rather
// than a parse failure.
func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("missing file yields composed defaults", func(t *testing.T) {
		t.Parallel()
		got, err := Load(filepath.Join(t.TempDir(), ".ergon.yaml"))
		if err != nil {
			t.Fatalf("Load(missing) err: %v", err)
		}
		if got.Name != "" {
			t.Fatalf("Name = %q, want empty", got.Name)
		}
		if len(got.Bootstrap.ExtraTools) != 0 {
			t.Fatalf("Bootstrap.ExtraTools = %+v, want empty", got.Bootstrap.ExtraTools)
		}
	})

	t.Run("empty file yields composed defaults", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, "")
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load(empty) err: %v", err)
		}
		if got.Name != "" {
			t.Fatalf("Name = %q, want empty", got.Name)
		}
	})

	t.Run("comments-only file yields composed defaults", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, "# nothing but a comment\n")
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load(comments-only) err: %v", err)
		}
		if got.Name != "" {
			t.Fatalf("Name = %q, want empty", got.Name)
		}
	})

	t.Run("name field parses from the file", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, "name: ergon\n")
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load err: %v", err)
		}
		if got.Name != "ergon" {
			t.Fatalf("Name = %q, want ergon", got.Name)
		}
	})

	t.Run("bootstrap pinned versions populate the map", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, `bootstrap:
  pinned:
    mvdan.cc/gofumpt: v0.6.0
    example.com/tool: v1.2.3
`)
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load err: %v", err)
		}
		if v := got.Bootstrap.Pinned["mvdan.cc/gofumpt"]; v != "v0.6.0" {
			t.Errorf("Pinned[gofumpt] = %q, want v0.6.0", v)
		}
		if v := got.Bootstrap.Pinned["example.com/tool"]; v != "v1.2.3" {
			t.Errorf("Pinned[example.com/tool] = %q, want v1.2.3", v)
		}
	})

	t.Run("bootstrap extras populate the slice in declared order", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, `bootstrap:
  extra_tools:
    - pkg: example.com/tool-a
      version: latest
    - pkg: example.com/tool-b
      version: v1.2.3
`)
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load err: %v", err)
		}
		want := []bootstrap.ToolSpec{
			{Pkg: "example.com/tool-a", Version: "latest"},
			{Pkg: "example.com/tool-b", Version: "v1.2.3"},
		}
		if len(got.Bootstrap.ExtraTools) != len(want) {
			t.Fatalf("ExtraTools = %+v, want %+v", got.Bootstrap.ExtraTools, want)
		}
		for i, w := range want {
			if got.Bootstrap.ExtraTools[i] != w {
				t.Fatalf("ExtraTools[%d] = %+v, want %+v", i, got.Bootstrap.ExtraTools[i], w)
			}
		}
	})

	t.Run("unknown top-level field surfaces ErrUnknownField", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, "name: ergon\nwidget: 7\n")
		_, err := Load(path)
		if !errors.Is(err, ErrUnknownField) {
			t.Fatalf("Load err = %v, want wrapped ErrUnknownField", err)
		}
	})

	t.Run("unknown nested field surfaces ErrUnknownField", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, "bootstrap:\n  unknown_key: 1\n")
		_, err := Load(path)
		if !errors.Is(err, ErrUnknownField) {
			t.Fatalf("Load err = %v, want wrapped ErrUnknownField", err)
		}
	})

	t.Run("release.modules populates the release scope", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, `release:
  modules:
    - .
    - cli
`)
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load err: %v", err)
		}
		want := []string{".", "cli"}
		if len(got.Release.Modules) != len(want) {
			t.Fatalf("Release.Modules = %+v, want %+v", got.Release.Modules, want)
		}
		for i, m := range want {
			if got.Release.Modules[i] != m {
				t.Errorf("Release.Modules[%d] = %q, want %q", i, got.Release.Modules[i], m)
			}
		}
	})

	t.Run("malformed yaml surfaces a non-ErrUnknownField error", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, "name: [unterminated\n")
		_, err := Load(path)
		if err == nil {
			t.Fatalf("Load returned nil error for malformed YAML")
		}
		if errors.Is(err, ErrUnknownField) {
			t.Fatalf("Load err = %v, want a parse error, not ErrUnknownField", err)
		}
	})

	t.Run("time.Duration fields parse Go-style literals", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, `test:
  timeout: 10m
  fuzz_time: 30s
`)
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load err: %v", err)
		}
		if got.Test.Timeout != 10*time.Minute {
			t.Errorf("Test.Timeout = %v, want 10m", got.Test.Timeout)
		}
		if got.Test.FuzzTime != 30*time.Second {
			t.Errorf("Test.FuzzTime = %v, want 30s", got.Test.FuzzTime)
		}
	})

	t.Run("invalid duration surfaces a non-ErrUnknownField error", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, "test:\n  timeout: not-a-duration\n")
		_, err := Load(path)
		if err == nil {
			t.Fatalf("Load returned nil error for invalid duration")
		}
		if errors.Is(err, ErrUnknownField) {
			t.Fatalf("Load err = %v, want a parse error, not ErrUnknownField", err)
		}
	})
}

// TestContainsUnknownField pins the pure substring detector that
// classifies goccy errors. The function lives at package scope so
// future goccy upgrades only touch one place; the test exercises
// both branches directly without constructing a goccy error
// fixture.
func TestContainsUnknownField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{
			name: "exact marker matches",
			msg:  "unknown field",
			want: true,
		},
		{
			name: "marker embedded in a wider message matches",
			msg:  `[3:3] unknown field "widget"`,
			want: true,
		},
		{
			name: "empty message does not match",
			msg:  "",
			want: false,
		},
		{
			name: "unrelated yaml error does not match",
			msg:  "unexpected character ']' at line 1",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := containsUnknownField(tc.msg); got != tc.want {
				t.Errorf("containsUnknownField(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

// writeYAML writes body to a fresh `.ergon.yaml` in a per-test
// temp dir and returns its path. Fails the test on write error.
func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".ergon.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
