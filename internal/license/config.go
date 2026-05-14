// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package license applies and verifies SPDX license headers across
// the repository's Go sources. Backed by Palantir's `go-license`
// tool; this package owns the file-set selection and the apply /
// verify split.
package license

// Config carries the per-repository overrides for [Apply] and
// [Verify]. The defaults provided by [Defaults] cover the
// directories Go convention keeps out of the source tree plus the
// generated-file suffixes ergon recognises; repos that need
// additional carve-outs append to [Config.ExcludeDirs] or
// [Config.ExcludeFiles].
type Config struct {
	// ConfigFile is the path to go-license's YAML config, relative
	// to the repository root. Defaults to `.go-license.yml`.
	ConfigFile string `yaml:"config_file"`

	// ExcludeDirs lists directory basenames to skip during the
	// source walk. The walker prunes the entire subtree below any
	// matching directory. Set explicitly in `.ergon.yaml` to
	// replace the default set; matching is by basename, not path,
	// so an entry like `vendor` excludes every `vendor/` directory
	// at any depth.
	ExcludeDirs []string `yaml:"exclude_dirs"`

	// ExcludeFiles lists glob patterns (matched against the file's
	// basename via [filepath.Match]) for sources to skip during
	// the walk. Used for generated-file suffixes; a repo with
	// project-specific generators (e.g. `*.pb.go`) appends to it.
	ExcludeFiles []string `yaml:"exclude_files"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the license section. The
// directory blocklist covers VCS internals (`.git`), vendored
// dependencies (`vendor`), build artefacts (`dist`), and JS
// package directories (`node_modules`). The file blocklist drops
// the suffix conventions for stringer-style code generators.
func Defaults() Config {
	return Config{
		ConfigFile:   ".go-license.yml",
		ExcludeDirs:  []string{".git", "vendor", "dist", "node_modules"},
		ExcludeFiles: []string{"*.gen.go", "*.gen_test.go"},
	}
}
