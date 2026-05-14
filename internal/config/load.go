// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// ErrUnknownField is returned when `.ergon.yaml` contains a key
// that does not map to a [Config] field. Wrapping a sentinel lets
// callers distinguish schema typos (their bug) from YAML-syntax or
// filesystem errors (environment problems) without inspecting error
// strings. The strict-unknown-key behaviour catches typos that
// would otherwise silently fall back to defaults.
var ErrUnknownField = errors.New("config: unknown field in .ergon.yaml")

// Load reads `.ergon.yaml` from path (or `.ergon.yaml` in the
// current working directory when path is empty) and layers its
// contents over [Defaults]. A missing file is not an error —
// discovery-first design means an unconfigured repository runs on
// defaults alone.
//
// The decoder is strict: any key in the file that does not map to
// a [Config] field returns an [ErrUnknownField]-wrapped error.
// YAML-syntax errors and read failures wrap the underlying error
// without [ErrUnknownField] so callers can distinguish the two
// classes of failure.
//
// Duration fields (e.g. [test.Config.Timeout]) accept Go-style
// duration literals (`10m`, `30s`, `1h30m`) via a [time.Duration]
// custom-unmarshal hook registered on the decoder. The hook is
// type-keyed, so any struct field declared as [time.Duration]
// participates without per-call wiring.
//
// Path resolution: when path is empty, Load reads `.ergon.yaml`
// from the current working directory. Callers that need a fixed
// search root (e.g. `git rev-parse --show-toplevel`) resolve the
// absolute path themselves and pass it in.
func Load(path string) (Config, error) {
	cfg := Defaults()
	resolved := resolvePath(path)

	data, err := os.ReadFile(resolved)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("config: read %s: %w", resolved, err)
	}

	// An empty file (or one containing only comments) is valid and
	// means "no overrides"; goccy's Unmarshal returns nil in that
	// case without modifying cfg, which is exactly the behaviour we
	// want. Distinguishing it from a parse error matters only for
	// the error path below.
	if err := yaml.UnmarshalWithOptions(
		data, &cfg,
		yaml.Strict(),
		yaml.CustomUnmarshaler[time.Duration](unmarshalDuration),
	); err != nil {
		if isUnknownFieldError(err) {
			return Config{}, fmt.Errorf("config: %s: %w: %w", resolved, ErrUnknownField, err)
		}
		return Config{}, fmt.Errorf("config: parse %s: %w", resolved, err)
	}
	return cfg, nil
}

// resolvePath returns the filesystem path [Load] reads from. An
// explicit path is honoured verbatim; the empty string resolves to
// `.ergon.yaml` in the current working directory. Exposed at
// package scope so its branches are independently testable.
func resolvePath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(".", ".ergon.yaml")
}

// unmarshalDuration decodes a scalar YAML node into a
// [time.Duration] via [time.ParseDuration]. Registered as a
// [yaml.CustomUnmarshaler] so every struct field typed
// [time.Duration] (e.g. [test.Config.Timeout]) accepts Go-style
// literals like `10m` and `30s` without callers wiring decoders
// per use site.
//
// goccy hands the raw scalar bytes verbatim — including any
// trailing newline goccy preserves from the source document and
// the surrounding quote characters when the scalar was quoted in
// YAML. The unmarshaller trims both so `10m`, `"10m"`, and `'10m'`
// all reach [time.ParseDuration] as the literal `10m`.
func unmarshalDuration(d *time.Duration, b []byte) error {
	s := strings.Trim(string(b), " \t\r\n\"'")
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = parsed
	return nil
}

// isUnknownFieldError reports whether err is goccy's signal that
// the YAML input contained a key the target struct does not
// declare. Goccy emits the literal substring `unknown field` for
// this case; we match on the substring so callers can wrap the
// error with [ErrUnknownField] without depending on goccy's
// internal types. The check stays in one place so a future goccy
// upgrade only touches this function.
func isUnknownFieldError(err error) bool {
	if err == nil {
		return false
	}
	return containsUnknownField(err.Error())
}

// containsUnknownField is the pure substring check, factored out
// so it is independently testable without constructing a goccy
// error.
func containsUnknownField(msg string) bool {
	const marker = "unknown field"
	for i := 0; i+len(marker) <= len(msg); i++ {
		if msg[i:i+len(marker)] == marker {
			return true
		}
	}
	return false
}
