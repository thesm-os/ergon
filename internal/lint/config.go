// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package lint

import "go.thesmos.sh/ergon/internal/checks/errorprefix"

// Config carries the per-repository overrides for `ergon lint`.
// The umbrella runs every built-in stage (`vet`, `go`, `md`,
// `license`, `skip-expiry`, `error-prefix`, `vuln`) by default;
// the fields here let the project narrow the set, configure
// individual stages, or both, without forking.
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
	// (e.g. a Go-only repo dropping `md` and `license`, or an
	// offline build dropping `vuln`).
	Disabled []string `yaml:"disabled"`

	// ErrorPrefix configures the `error-prefix` stage. Lives
	// under `lint` rather than at the top level because the
	// stage is a lint (AST scan over source) and the config
	// surface should mirror that.
	ErrorPrefix errorprefix.Config `yaml:"error_prefix"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the lint section. The filter
// fields stay empty (every built-in stage runs); per-stage
// configs inherit each stage's own [errorprefix.Defaults] etc.
func Defaults() Config {
	return Config{
		ErrorPrefix: errorprefix.Defaults(),
	}
}
