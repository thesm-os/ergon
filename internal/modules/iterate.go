// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package modules

import (
	"context"
	"fmt"
)

// Iterate runs fn against each module sequentially. Returns the
// first error fn produces, wrapped with the module's [Module.Dir]
// for context so the caller can identify which module the failure
// belongs to. Iteration stops at the first failure — callers that
// need keep-going semantics use [IterateContinue].
//
// Iterate carries no subprocess machinery; fn is responsible for
// whatever the per-module work is (shelling out, reading files,
// inspecting `go.mod`). This keeps the helper trivially composable
// — every multi-module command in ergon is shaped as a single
// Iterate call.
func Iterate(ctx context.Context, mods []Module, fn func(context.Context, Module) error) error {
	for _, m := range mods {
		if err := fn(ctx, m); err != nil {
			return fmt.Errorf("[%s]: %w", m.Dir, err)
		}
	}
	return nil
}

// Result records the outcome of one module's fn invocation. Used
// by [IterateContinue] so callers can render per-module verdicts
// in a closing summary block.
type Result struct {
	// Module is the module fn was invoked against.
	Module Module

	// Err is the error fn returned, or nil on success.
	Err error
}

// IterateContinue runs fn against every module and returns one
// [Result] per module, regardless of whether intermediate
// invocations succeed or fail. Unlike [Iterate], this never
// short-circuits — every module's fn runs to completion so the
// caller can produce a single aggregated report.
//
// The returned slice preserves input order and has exactly
// len(mods) entries.
func IterateContinue(ctx context.Context, mods []Module, fn func(context.Context, Module) error) []Result {
	out := make([]Result, 0, len(mods))
	for _, m := range mods {
		out = append(out, Result{Module: m, Err: fn(ctx, m)})
	}
	return out
}
