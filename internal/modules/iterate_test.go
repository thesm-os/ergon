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
