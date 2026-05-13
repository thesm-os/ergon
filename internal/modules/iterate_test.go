// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package modules

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestIterate pins the helper's contract: ordered traversal, the
// per-module function receives the cancelling context, errors halt
// the run and surface with the offending module's directory in the
// wrapper.
func TestIterate(t *testing.T) {
	t.Parallel()

	t.Run("visits every module in receive order", func(t *testing.T) {
		t.Parallel()
		mods := []Module{{Dir: "."}, {Dir: "cli"}, {Dir: "frontend/golang"}}
		var visited []string
		err := Iterate(t.Context(), mods, func(_ context.Context, m Module) error {
			visited = append(visited, m.Dir)
			return nil
		})
		if err != nil {
			t.Fatalf("Iterate err: %v", err)
		}
		want := []string{".", "cli", "frontend/golang"}
		if !slices.Equal(visited, want) {
			t.Fatalf("visited = %+v, want %+v", visited, want)
		}
	})

	t.Run("first error halts the run", func(t *testing.T) {
		t.Parallel()
		mods := []Module{{Dir: "a"}, {Dir: "b"}, {Dir: "c"}}
		var visited []string
		sentinel := errors.New("stop")
		err := Iterate(t.Context(), mods, func(_ context.Context, m Module) error {
			visited = append(visited, m.Dir)
			if m.Dir == "b" {
				return sentinel
			}
			return nil
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("Iterate err = %v, want sentinel wrapped", err)
		}
		want := []string{"a", "b"}
		if !slices.Equal(visited, want) {
			t.Fatalf("visited = %+v, want %+v (stop after first failure)", visited, want)
		}
	})

	t.Run("error wrapper names the failing module's dir", func(t *testing.T) {
		t.Parallel()
		mods := []Module{{Dir: "frontend/golang"}}
		err := Iterate(t.Context(), mods, func(_ context.Context, _ Module) error {
			return errors.New("boom")
		})
		if err == nil {
			t.Fatalf("Iterate returned nil, want wrapped error")
		}
		if !strings.Contains(err.Error(), "[frontend/golang]") {
			t.Fatalf("err = %v, want it to mention [frontend/golang]", err)
		}
	})

	t.Run("empty module slice yields nil", func(t *testing.T) {
		t.Parallel()
		err := Iterate(t.Context(), nil, func(_ context.Context, _ Module) error {
			t.Fatal("fn called with empty input")
			return nil
		})
		if err != nil {
			t.Fatalf("Iterate(empty) err: %v", err)
		}
	})
}

// TestIterateContinue pins the collect-and-continue semantics: fn
// runs once per module regardless of intermediate failures, and
// the result slice preserves input order and length.
func TestIterateContinue(t *testing.T) {
	t.Parallel()

	t.Run("runs every module and records each outcome in order", func(t *testing.T) {
		t.Parallel()
		mods := []Module{{Dir: "a"}, {Dir: "b"}, {Dir: "c"}}
		results := IterateContinue(t.Context(), mods, func(_ context.Context, m Module) error {
			if m.Dir == "b" {
				return errors.New("middle fail")
			}
			return nil
		})
		if len(results) != 3 {
			t.Fatalf("len(results) = %d, want 3", len(results))
		}
		if results[0].Err != nil || results[2].Err != nil {
			t.Fatalf("results = %+v, want a, c to be nil-err", results)
		}
		if results[1].Err == nil {
			t.Fatalf("results[1] = %+v, want non-nil err for b", results[1])
		}
		if results[0].Module.Dir != "a" || results[1].Module.Dir != "b" || results[2].Module.Dir != "c" {
			t.Fatalf("results module order = %+v, want a/b/c", results)
		}
	})

	t.Run("empty slice yields an empty results slice", func(t *testing.T) {
		t.Parallel()
		got := IterateContinue(t.Context(), nil, func(_ context.Context, _ Module) error {
			t.Fatal("fn called with empty input")
			return nil
		})
		if len(got) != 0 {
			t.Fatalf("results = %+v, want empty", got)
		}
	})
}
