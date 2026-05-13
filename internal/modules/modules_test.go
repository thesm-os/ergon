// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package modules

import "testing"

// TestModuleTagPrefix pins the tag-name composition rule the rest
// of the toolchain builds on top of. The rule is uniform: root has
// no prefix, everything else takes its relative directory plus a
// trailing slash.
func TestModuleTagPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dir  string
		want string
	}{
		{".", ""},
		{"cli", "cli/"},
		{"frontend/golang", "frontend/golang/"},
		{"deeply/nested/module", "deeply/nested/module/"},
	}
	for _, tc := range cases {
		t.Run(tc.dir+" composes tag prefix per the documented rule", func(t *testing.T) {
			t.Parallel()
			got := Module{Dir: tc.dir}.TagPrefix()
			if got != tc.want {
				t.Fatalf("TagPrefix = %q, want %q", got, tc.want)
			}
		})
	}
}
