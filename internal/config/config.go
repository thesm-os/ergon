// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package config composes the ergon-wide configuration from the
// per-subsystem configs each `internal/<subsystem>` package owns.
// The cobra entrypoint calls [Load] to read `.ergon.yaml` over the
// composed defaults; subcommands consume the relevant section of
// the returned [Config].
//
// This package deliberately holds no per-subsystem schema:
// `internal/bootstrap.Config`, `internal/test.Config`, etc. live
// alongside the code that consumes them so new commands extend the
// schema by adding a field here rather than by editing a global.
package config

import (
	"go.thesmos.sh/ergon/internal/bootstrap"
)

// Config is the fully-resolved configuration for one ergon
// invocation. Each field embeds a subsystem package's own Config
// type so the schema stays close to the code that reads it.
//
// Fields left empty after [Load] returns remain candidates for
// runtime discovery — an empty [Config.Name] gets filled with the
// basename of the repository root, for example.
type Config struct {
	// Name identifies the project. Drives the directory under
	// which ergon writes coverage and build artifacts (`.ergon/`,
	// `.eidos/`, ...) and appears in help banners. Defaults at
	// runtime to the basename of the repository root.
	Name string `mapstructure:"name"`

	// Bootstrap configures `ergon bootstrap`. See
	// [bootstrap.Config] for field semantics.
	Bootstrap bootstrap.Config `mapstructure:"bootstrap"`
}

// Defaults returns the Config populated with each subsystem's own
// defaults. The cobra entrypoint layers `.ergon.yaml`'s parsed
// contents over the returned value, so any field absent from the
// file keeps the subsystem-provided default.
func Defaults() Config {
	return Config{
		Bootstrap: bootstrap.Defaults(),
	}
}
