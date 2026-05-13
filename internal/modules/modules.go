// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package modules defines the [Module] value type ergon iterates
// when running per-module tasks (test, lint, tidy, release). The
// type carries the directory of one Go module and exposes the
// tag-prefix rule the rest of the toolchain follows.
//
// The package holds no discovery logic — `internal/discover`
// produces []Module from `go.work` or a filesystem walk. Keeping
// the type here lets release/test/bench code reference Module
// without taking a dependency on the discovery machinery.
package modules

// Module is one of a repository's Go modules. Discovery resolves
// the list from `go.work` (or a single-root fallback when no
// workspace is present); commands then iterate the slice.
type Module struct {
	// Dir is the module's directory relative to the repository
	// root. `.` denotes the root module itself; `cli` denotes a
	// module nested directly under the root; `frontend/golang`
	// denotes a deeper submodule.
	Dir string
}

// TagPrefix returns the leading string every tag for this module
// shares. Root produces `""` so the tag is the bare `v1.2.3`; a
// submodule at `foo/bar` produces `foo/bar/` so the tag becomes
// `foo/bar/v1.2.3`, per Go's multi-module convention.
func (m Module) TagPrefix() string {
	if m.Dir == "." {
		return ""
	}
	return m.Dir + "/"
}
