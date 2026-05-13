// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// ErrUnknownField wraps viper's "unused key" error so callers can
// distinguish a malformed `.ergon.yaml` (caller bug) from a viper
// read failure (filesystem / encoding issue) without inspecting
// error strings.
var ErrUnknownField = errors.New("unknown field in .ergon.yaml")

// Load reads `.ergon.yaml` from the repository root (or from the
// explicit path when non-empty) and layers its contents over
// [Defaults]. A missing file is not an error — discovery-first
// design means an unconfigured repository runs on defaults alone.
//
// The decoder is strict: any key in the file that does not map to
// a [Config] field returns an [ErrUnknownField]-wrapped error.
// This catches typos that would otherwise silently fall back to
// defaults.
//
// Path resolution: when path is empty, Load searches the current
// working directory for `.ergon.yaml`. Callers that need a fixed
// search root (e.g. `git rev-parse --show-toplevel`) pass the
// resolved absolute path.
func Load(path string) (Config, error) {
	cfg := Defaults()

	v := viper.New()
	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName(".ergon")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		switch {
		case errors.As(err, &notFound), errors.Is(err, fs.ErrNotExist):
			return cfg, nil
		default:
			return Config{}, fmt.Errorf("config: read %s: %w", configPathFor(v, path), err)
		}
	}

	opt := func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
	}
	if err := v.Unmarshal(&cfg, opt); err != nil {
		return Config{}, fmt.Errorf("config: %w: %w", ErrUnknownField, err)
	}
	return cfg, nil
}

// configPathFor returns a human-facing path for the config file the
// loader was operating on. Used only inside error messages so the
// user can see which file produced the read failure.
func configPathFor(v *viper.Viper, requested string) string {
	if used := v.ConfigFileUsed(); used != "" {
		return used
	}
	if requested != "" {
		return requested
	}
	return ".ergon.yaml"
}
