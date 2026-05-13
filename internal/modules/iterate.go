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
// need keep-going semantics build them on top.
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
