// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package errorprefix enforces the convention that every
// `errors.New("...")` sentinel starts with the file's package
// name (or a `<pkg>.<sub>:` qualifier for sub-packages). The
// rule keeps sentinel messages self-identifying when surfaced
// far from the point of definition.
package errorprefix

// Config carries the per-repository overrides for [Run]. The
// default scan root is the working tree below the repository
// root; repos that want to narrow scope (e.g. only the library
// layers, not cmd/) override [Config.TargetDirs].
type Config struct {
	// TargetDirs is the list of directories (relative to the
	// repository root) to scan. The walker recurses into each.
	// Defaults to `[.]` (the entire repository).
	TargetDirs []string `mapstructure:"target_dirs"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the error-prefix section.
func Defaults() Config {
	return Config{
		TargetDirs: []string{"."},
	}
}
