// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package stage

import (
	"errors"
	"fmt"
)

// ErrUnknownStage signals that a [Filter] referenced a stage name
// that does not appear in the input set. Returned by [Filter.Apply]
// so callers can surface it as a usage error at the cobra layer
// rather than silently no-op'ing a typo.
var ErrUnknownStage = errors.New("stage: unknown stage name")

// Named pairs a stage's identifier with the function that runs it.
// Used by the check and lint umbrellas to compose orderable,
// filterable stage lists.
//
// Order matters: callers build the slice in the canonical
// execution order they want preserved through filtering, and
// [Filter.Apply] returns survivors in that same order. The
// renderer relies on this so the report reads predictably.
type Named struct {
	// Name is the identifier the user writes in `.ergon.yaml`
	// (`lint.enabled: [vet, go]`) or on the CLI
	// (`--only lint,test`). Must be unique within one stage list.
	Name string

	// Run executes the stage and returns its failure (or nil on
	// pass). The umbrella records failures and renders the
	// aggregated verdict block.
	Run func() error
}

// Filter is the policy that selects which [Named] stages
// participate in a run. The struct mirrors the YAML side
// ([Filter.Enabled], [Filter.Disabled]) plus the per-invocation
// CLI overrides ([Filter.Only], [Filter.Skip]).
//
// Precedence (highest first):
//
//   - Only (CLI): wins absolutely. When non-empty, every other
//     field is ignored and exactly these stages run. This lets
//     a user override a restrictive config without editing the
//     file.
//   - Skip (CLI) + Disabled (config): union; any stage named in
//     either is removed from the run.
//   - Enabled (config): when non-empty, restricts the starting
//     set to these names; empty means "every input stage is in
//     scope" (the default).
//
// All names are matched against [Named.Name] verbatim. An
// unrecognised name in any field surfaces [ErrUnknownStage] from
// [Filter.Apply] — typos are the most common authoring mistake
// and a silent no-op would mask them.
type Filter struct {
	// Enabled is the config-side allowlist. Empty means
	// "no restriction" (the default).
	Enabled []string

	// Disabled is the config-side denylist applied after Enabled.
	Disabled []string

	// Only is the CLI allowlist (--only). When non-empty it
	// overrides every other field; useful for "run just this one
	// stage" ergonomic without editing config.
	Only []string

	// Skip is the CLI denylist (--skip). Unions with Disabled
	// when both apply.
	Skip []string
}

// Apply returns the subset of stages that survive the filter,
// preserving input order. Returns [ErrUnknownStage] when any name
// in the filter has no matching stage; the error wraps the offending
// name so the caller can surface it directly.
func (f Filter) Apply(stages []Named) ([]Named, error) {
	known := make(map[string]bool, len(stages))
	for _, s := range stages {
		known[s.Name] = true
	}
	for _, list := range [][]string{f.Enabled, f.Disabled, f.Only, f.Skip} {
		for _, n := range list {
			if !known[n] {
				return nil, fmt.Errorf("%w: %q", ErrUnknownStage, n)
			}
		}
	}

	// --only wins absolutely; the other fields are ignored when set.
	if len(f.Only) > 0 {
		allow := setOf(f.Only)
		out := make([]Named, 0, len(f.Only))
		for _, s := range stages {
			if allow[s.Name] {
				out = append(out, s)
			}
		}
		return out, nil
	}

	allow := known
	if len(f.Enabled) > 0 {
		allow = setOf(f.Enabled)
	}
	deny := setOf(f.Disabled)
	for _, n := range f.Skip {
		deny[n] = true
	}
	out := make([]Named, 0, len(stages))
	for _, s := range stages {
		if allow[s.Name] && !deny[s.Name] {
			out = append(out, s)
		}
	}
	return out, nil
}

// setOf returns the set membership form of a string slice. The
// empty input yields an empty (non-nil) map so callers can `range`
// or look up without nil checks.
func setOf(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
