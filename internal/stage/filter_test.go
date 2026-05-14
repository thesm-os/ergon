// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package stage

import (
	"errors"
	"testing"
)

// TestFilterApply pins the precedence rules and the order-
// preservation contract of [Filter.Apply]:
//
//   - Only (CLI) wins absolutely.
//   - Skip and Disabled union as denies.
//   - Enabled restricts the starting set; empty means "everything".
//   - Input order is preserved in the result.
//   - Unknown names in any field surface [ErrUnknownStage].
func TestFilterApply(t *testing.T) {
	t.Parallel()

	// stages is the canonical input — its declared order is what
	// every subtest expects to see preserved in the output.
	stages := []Named{
		{Name: "a"},
		{Name: "b"},
		{Name: "c"},
		{Name: "d"},
	}

	cases := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{
			name:   "empty filter passes every stage through",
			filter: Filter{},
			want:   []string{"a", "b", "c", "d"},
		},
		{
			name:   "Enabled restricts to the listed stages",
			filter: Filter{Enabled: []string{"a", "c"}},
			want:   []string{"a", "c"},
		},
		{
			name:   "Disabled removes the listed stages",
			filter: Filter{Disabled: []string{"b"}},
			want:   []string{"a", "c", "d"},
		},
		{
			name:   "Enabled minus Disabled is the intersection logic",
			filter: Filter{Enabled: []string{"a", "b", "c"}, Disabled: []string{"b"}},
			want:   []string{"a", "c"},
		},
		{
			name:   "Skip unions with Disabled",
			filter: Filter{Disabled: []string{"b"}, Skip: []string{"d"}},
			want:   []string{"a", "c"},
		},
		{
			name:   "Only beats Enabled",
			filter: Filter{Enabled: []string{"a"}, Only: []string{"b", "c"}},
			want:   []string{"b", "c"},
		},
		{
			name:   "Only beats Disabled",
			filter: Filter{Disabled: []string{"b"}, Only: []string{"b"}},
			want:   []string{"b"},
		},
		{
			name:   "Only beats Skip",
			filter: Filter{Skip: []string{"b"}, Only: []string{"b"}},
			want:   []string{"b"},
		},
		{
			name:   "Only preserves input order, not Only's declared order",
			filter: Filter{Only: []string{"d", "a"}},
			want:   []string{"a", "d"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.filter.Apply(stages)
			if err != nil {
				t.Fatalf("Apply err: %v", err)
			}
			gotNames := make([]string, len(got))
			for i, s := range got {
				gotNames[i] = s.Name
			}
			if len(gotNames) != len(tc.want) {
				t.Fatalf("Apply = %v, want %v", gotNames, tc.want)
			}
			for i, w := range tc.want {
				if gotNames[i] != w {
					t.Fatalf("Apply[%d] = %q, want %q (full: %v)", i, gotNames[i], w, gotNames)
				}
			}
		})
	}
}

// TestFilterApplyUnknownStage pins the typo-catching behaviour:
// every field validates against the input set, and the offending
// name appears in the error so the user can locate the typo.
func TestFilterApplyUnknownStage(t *testing.T) {
	t.Parallel()

	stages := []Named{{Name: "a"}, {Name: "b"}}

	cases := []struct {
		name   string
		filter Filter
	}{
		{name: "unknown Enabled", filter: Filter{Enabled: []string{"x"}}},
		{name: "unknown Disabled", filter: Filter{Disabled: []string{"x"}}},
		{name: "unknown Only", filter: Filter{Only: []string{"x"}}},
		{name: "unknown Skip", filter: Filter{Skip: []string{"x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.filter.Apply(stages)
			if err == nil {
				t.Fatalf("Apply returned nil, want ErrUnknownStage")
			}
			if !errors.Is(err, ErrUnknownStage) {
				t.Fatalf("err = %v, want wrapped ErrUnknownStage", err)
			}
		})
	}
}

// TestFilterApplyRunsSurvivors confirms the [Named.Run] closure
// captured in each surviving entry is callable post-filter — the
// filter must not strip or replace the Run field.
func TestFilterApplyRunsSurvivors(t *testing.T) {
	t.Parallel()

	called := map[string]int{}
	stages := []Named{
		{Name: "a", Run: func() error { called["a"]++; return nil }},
		{Name: "b", Run: func() error { called["b"]++; return nil }},
	}
	got, err := Filter{Only: []string{"b"}}.Apply(stages)
	if err != nil {
		t.Fatalf("Apply err: %v", err)
	}
	for _, s := range got {
		_ = s.Run()
	}
	if called["a"] != 0 {
		t.Errorf("stage a ran %d times, want 0", called["a"])
	}
	if called["b"] != 1 {
		t.Errorf("stage b ran %d times, want 1", called["b"])
	}
}
