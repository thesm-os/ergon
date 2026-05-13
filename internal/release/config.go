// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

// Config carries the release-specific knobs `.ergon.yaml`'s
// `release:` section maps to. Distinct from [Options], which is
// the per-invocation flag bundle the cobra layer builds.
type Config struct {
	// Modules optionally scopes `ergon release` to a subset of
	// modules. When non-empty, the list REPLACES the global
	// `.ergon.yaml` `modules:` value for release only — other
	// subcommands (test, lint, bench, ...) keep operating on the
	// global set. When empty, release inherits the global module
	// list, which itself falls back to `go.work` discovery.
	//
	// Paths follow the same conventions as the global field:
	// repository-relative, `.` for the root module, no leading
	// `./`.
	Modules []string `mapstructure:"modules"`
}

// Defaults returns the zero-value [Config]. Release has no defaults
// to populate — an empty Modules list means "inherit the global
// module set" which is the documented contract.
func Defaults() Config {
	return Config{}
}
