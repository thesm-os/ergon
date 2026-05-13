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
	"go.thesmos.sh/ergon/internal/checks/commitmsg"
	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	"go.thesmos.sh/ergon/internal/checks/mutation"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/test"
)

// Config is the fully-resolved configuration for one ergon
// invocation. Each field embeds a subsystem package's own Config
// type so the schema stays close to the code that reads it.
//
// Fields left empty after [Load] returns remain candidates for
// runtime discovery — an empty [Config.Name] gets filled with the
// basename of the repository root, an empty [Config.Modules] gets
// populated from `go.work`, etc.
type Config struct {
	// Name identifies the project. Drives the directory under
	// which ergon writes coverage and build artifacts (`.ergon/`,
	// `.eidos/`, ...) and appears in help banners. Defaults at
	// runtime to the basename of the repository root.
	Name string `mapstructure:"name"`

	// Modules optionally fixes the module set ergon iterates,
	// bypassing `go.work` discovery. Paths are relative to the
	// repository root; `.` denotes the root module. When empty,
	// discovery reads `go.work` (or falls back to a single root
	// entry).
	Modules []string `mapstructure:"modules"`

	// Bootstrap configures `ergon bootstrap`. See
	// [bootstrap.Config] for field semantics.
	Bootstrap bootstrap.Config `mapstructure:"bootstrap"`

	// License configures `ergon license` (apply) and
	// `ergon lint license` (verify). See [license.Config] for
	// field semantics.
	License license.Config `mapstructure:"license"`

	// Markdown configures the markdownlint-cli2 invocation `ergon
	// fmt` and `ergon lint md` share. See [markdown.Config] for
	// field semantics.
	Markdown markdown.Config `mapstructure:"markdown"`

	// Test configures `ergon test` and its subcommands. See
	// [test.Config] for field semantics.
	Test test.Config `mapstructure:"test"`

	// Checks configures the `ergon check *` subcommands.
	Checks ChecksConfig `mapstructure:"checks"`
}

// ChecksConfig groups the per-check configs so each subsystem's
// settings live under a single top-level YAML key (`checks:`) the
// way the design's example shows.
type ChecksConfig struct {
	// Coverage configures `ergon check coverage`. See
	// [coverage.Config].
	Coverage coverage.Config `mapstructure:"coverage"`

	// Mutation configures `ergon check mutation`. See
	// [mutation.Config].
	Mutation mutation.Config `mapstructure:"mutation"`

	// ErrorPrefix configures `ergon check error-prefix`. See
	// [errorprefix.Config].
	ErrorPrefix errorprefix.Config `mapstructure:"error_prefix"`

	// CommitMsg configures `ergon check commit-msg`. See
	// [commitmsg.Config].
	CommitMsg commitmsg.Config `mapstructure:"commit_msg"`
}

// Defaults returns the Config populated with each subsystem's own
// defaults. The cobra entrypoint layers `.ergon.yaml`'s parsed
// contents over the returned value, so any field absent from the
// file keeps the subsystem-provided default.
func Defaults() Config {
	return Config{
		Bootstrap: bootstrap.Defaults(),
		License:   license.Defaults(),
		Markdown:  markdown.Defaults(),
		Test:      test.Defaults(),
		Checks: ChecksConfig{
			Coverage:    coverage.Defaults(),
			Mutation:    mutation.Defaults(),
			ErrorPrefix: errorprefix.Defaults(),
			CommitMsg:   commitmsg.Defaults(),
		},
	}
}
