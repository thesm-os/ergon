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
	"go.thesmos.sh/ergon/internal/bench"
	"go.thesmos.sh/ergon/internal/bootstrap"
	"go.thesmos.sh/ergon/internal/checks/commitmsg"
	"go.thesmos.sh/ergon/internal/checks/coverage"
	"go.thesmos.sh/ergon/internal/checks/errorprefix"
	"go.thesmos.sh/ergon/internal/checks/mutation"
	"go.thesmos.sh/ergon/internal/checks/policy"
	"go.thesmos.sh/ergon/internal/license"
	"go.thesmos.sh/ergon/internal/lint"
	"go.thesmos.sh/ergon/internal/markdown"
	"go.thesmos.sh/ergon/internal/release"
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
	Name string `yaml:"name"`

	// Modules optionally fixes the module set ergon iterates,
	// bypassing `go.work` discovery. Paths are relative to the
	// repository root; `.` denotes the root module. When empty,
	// discovery reads `go.work` (or falls back to a single root
	// entry).
	Modules []string `yaml:"modules"`

	// Bootstrap configures `ergon bootstrap`. See
	// [bootstrap.Config] for field semantics.
	Bootstrap bootstrap.Config `yaml:"bootstrap"`

	// License configures `ergon license` (apply) and
	// `ergon lint license` (verify). See [license.Config] for
	// field semantics.
	License license.Config `yaml:"license"`

	// Markdown configures the markdownlint-cli2 invocation `ergon
	// fmt` and `ergon lint md` share. See [markdown.Config] for
	// field semantics.
	Markdown markdown.Config `yaml:"markdown"`

	// Lint configures the `ergon lint` umbrella. Today the only
	// fields are the stage allow/denylist; per-stage tool config
	// lives under each tool's own section ([Markdown], [License])
	// and is consumed by [lint.All] directly.
	Lint lint.Config `yaml:"lint"`

	// Test configures `ergon test` and its subcommands. See
	// [test.Config] for field semantics.
	Test test.Config `yaml:"test"`

	// Bench configures `ergon bench baseline` and `ergon bench
	// regression`. See [bench.Config].
	Bench bench.Config `yaml:"bench"`

	// Release configures `ergon release`. See [release.Config];
	// the only field today is a module scope that overrides
	// [Config.Modules] for release only.
	Release release.Config `yaml:"release"`

	// Checks configures the `ergon check *` subcommands.
	Checks ChecksConfig `yaml:"checks"`
}

// ChecksConfig groups the per-check configs so each subsystem's
// settings live under a single top-level YAML key (`checks:`) the
// way the design's example shows.
//
// Excludes and Skips live at this level — not under coverage or
// mutation — because both gates read the same rules: a generated
// file or conformance-suite entry that is exempt from one is
// invariably exempt from the other. See [policy.Exclude] and
// [policy.Skip].
//
// Enabled and Disabled are the umbrella's stage filter. The
// `ergon check` umbrella iterates a fixed sequence of named
// stages (mod, lint, test, coverage, skip-expiry, error-prefix,
// vuln, and the opt-in mutation/branch gates); these two fields
// let the project narrow the set. CLI flags `--only` / `--skip`
// layer on top per the precedence rules documented on
// [go.thesmos.sh/ergon/internal/stage.Filter].
type ChecksConfig struct {
	// Enabled, when non-empty, restricts `ergon check` to these
	// stages. Empty means "every default stage in scope" plus the
	// opt-in mutation/branch gates when their thresholds are
	// declared elsewhere in this section.
	Enabled []string `yaml:"enabled"`

	// Disabled removes the named stages from the run. Combines
	// with `--skip` on the CLI as a single denylist.
	Disabled []string `yaml:"disabled"`

	// Excludes is the shared path-exclusion list both coverage
	// and mutation consult. A path matching any [policy.Exclude]
	// is dropped from the coverage threshold check and excluded
	// from gremlins via `--exclude-files`.
	Excludes []policy.Exclude `yaml:"excludes"`

	// Skips is the shared structural-skip list both gates
	// consult. Coverage applies the (FuncGlob, FileGlob) pair
	// function-by-function; mutation, lacking a per-function
	// exclusion knob in gremlins, applies the FileGlob alone.
	Skips []policy.Skip `yaml:"skips"`

	// Coverage configures `ergon check coverage`. See
	// [coverage.Config].
	Coverage coverage.Config `yaml:"coverage"`

	// Mutation configures `ergon check mutation`. See
	// [mutation.Config].
	Mutation mutation.Config `yaml:"mutation"`

	// ErrorPrefix configures `ergon check error-prefix`. See
	// [errorprefix.Config].
	ErrorPrefix errorprefix.Config `yaml:"error_prefix"`

	// CommitMsg configures `ergon check commit-msg`. See
	// [commitmsg.Config].
	CommitMsg commitmsg.Config `yaml:"commit_msg"`
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
		Lint:      lint.Defaults(),
		Test:      test.Defaults(),
		Bench:     bench.Defaults(),
		Release:   release.Defaults(),
		Checks: ChecksConfig{
			Coverage:    coverage.Defaults(),
			Mutation:    mutation.Defaults(),
			ErrorPrefix: errorprefix.Defaults(),
			CommitMsg:   commitmsg.Defaults(),
		},
	}
}
