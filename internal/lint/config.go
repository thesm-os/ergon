// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package lint

// Config carries the per-repository overrides for `ergon lint`.
// The umbrella runs every built-in stage (`vet`, `go`, `md`,
// `license`) by default; the fields here let the project narrow
// or extend the set without forking.
//
// CLI flags (`--only`, `--skip` on `ergon lint`) layer on top of
// the config values per the precedence rules documented on
// [go.thesmos.sh/ergon/internal/stage.Filter].
type Config struct {
	// Enabled, when non-empty, restricts `ergon lint` to these
	// stages. Empty means "every built-in stage in scope" — the
	// default and the right answer for most repositories.
	Enabled []string `yaml:"enabled"`

	// Disabled removes the named stages from the run. Combines
	// with `--skip` on the CLI as a single denylist. Useful for
	// repositories that legitimately do not want a default stage
	// (e.g. a Go-only repo dropping `md` and `license`).
	Disabled []string `yaml:"disabled"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the lint section. The default
// is intentionally empty: every built-in stage runs.
func Defaults() Config {
	return Config{}
}
