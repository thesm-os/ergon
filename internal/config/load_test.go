// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"go.thesmos.sh/ergon/internal/bootstrap"
)

// TestConfigPathFor pins the error-message path resolver across
// its three branches: viper has a known file, no viper file but a
// requested path, neither.
func TestConfigPathFor(t *testing.T) {
	t.Parallel()

	t.Run("falls back to the literal default", func(t *testing.T) {
		t.Parallel()
		if got := configPathFor(viper.New(), ""); got != ".ergon.yaml" {
			t.Errorf("configPathFor = %q, want .ergon.yaml", got)
		}
	})

	t.Run("returns the requested path when viper has nothing", func(t *testing.T) {
		t.Parallel()
		if got := configPathFor(viper.New(), "/custom.yaml"); got != "/custom.yaml" {
			t.Errorf("configPathFor = %q, want /custom.yaml", got)
		}
	})

	t.Run("returns the viper-resolved path when present", func(t *testing.T) {
		t.Parallel()
		v := viper.New()
		v.SetConfigFile("/from-viper.yaml")
		if got := configPathFor(v, "/from-flag.yaml"); got != "/from-viper.yaml" {
			t.Errorf("configPathFor = %q, want /from-viper.yaml", got)
		}
	})
}

// TestLoad pins the loader's contract: defaults apply when no file
// is present, fields parsed from the file override the defaults,
// unknown fields surface as [ErrUnknownField], and a malformed file
// surfaces a wrapped viper error.
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

	t.Run("malformed yaml surfaces a read error", func(t *testing.T) {
		t.Parallel()
		path := writeYAML(t, "name: [unterminated\n")
		_, err := Load(path)
		if err == nil {
			t.Fatalf("Load returned nil error for malformed YAML")
		}
		if errors.Is(err, ErrUnknownField) {
			t.Fatalf("Load err = %v, want a read error, not ErrUnknownField", err)
		}
	})
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
