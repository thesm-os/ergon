// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package bootstrap installs the development tools every other
// ergon subcommand shells out to. The package owns the canonical
// tool list and the per-tool install policy; `cmd/ergon/cmds/`
// only wires the cobra surface around it.
package bootstrap

// Config carries the per-repository overrides for [Run]. The
// standard tool list is built into the binary; this struct names
// project-specific additions ([Config.ExtraTools]) and version
// overrides ([Config.Pinned]) layered on top. Repositories that
// want neither leave the section out of `.ergon.yaml` entirely
// and run on the built-in list at `latest`.
type Config struct {
	// ExtraTools is the per-repo list of additional Go tools to
	// install after the standard suite. Order is preserved; failures
	// abort the run at the first broken tool so partial states are
	// easy to reason about.
	ExtraTools []ToolSpec `yaml:"extra_tools"`

	// Pinned overrides the version `go install` uses for any tool
	// whose [ToolSpec.Pkg] appears as a key in the map. Applies to
	// both [DefaultTools] (which ship at `latest`) and
	// [Config.ExtraTools] (which carry their own
	// [ToolSpec.Version]); a matching entry here wins over either.
	//
	// Use Pinned to make CI tool installs deterministic without
	// forking the binary's default list. Local dev environments can
	// leave Pinned empty and ride `latest`. A typo'd or otherwise
	// unrecognised package in Pinned is silently ignored — the
	// surface is purely additive so introducing a pin for a tool
	// you are about to add to ExtraTools works without ordering
	// constraints.
	Pinned map[string]string `yaml:"pinned"`
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
