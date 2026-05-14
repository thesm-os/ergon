// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package bootstrap installs the development tools every other
// ergon subcommand shells out to. The package owns the canonical
// tool list and the per-tool install policy; `cmd/ergon/cmds/`
// only wires the cobra surface around it.
package bootstrap

// Config carries the per-repository overrides for [Run]. The
// standard tool list is built into the binary; this struct only
// names project-specific *additions*. Repositories that need none
// leave the section out of `.ergon.yaml` entirely and run on the
// built-in list alone.
type Config struct {
	// ExtraTools is the per-repo list of additional Go tools to
	// install after the standard suite. Order is preserved; failures
	// abort the run at the first broken tool so partial states are
	// easy to reason about.
	ExtraTools []ToolSpec `yaml:"extra_tools"`
}

// ToolSpec names one Go tool installable via `go install`. Pkg is
// the module path of the command; Version is the suffix applied
// after `@` (commonly `latest` for development environments and a
// pinned semver tag for repeatable builds).
type ToolSpec struct {
	// Pkg is the import path passed to `go install`.
	Pkg string `yaml:"pkg"`

	// Version is the suffix after `@`. An empty value is treated
	// as `latest` so callers can omit the field for unpinned dev
	// tooling.
	Version string `yaml:"version"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the bootstrap section. The
// default is intentionally empty: every repository starts from the
// built-in tool list ([DefaultTools]) and adds nothing.
func Defaults() Config {
	return Config{}
}
