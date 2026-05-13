// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import "testing"

// TestDefaults pins the zero-value contract: an unconfigured
// repository inherits the global module list (Modules empty).
func TestDefaults(t *testing.T) {
	t.Parallel()

	got := Defaults()
	if len(got.Modules) != 0 {
		t.Fatalf("Defaults().Modules = %+v, want empty", got.Modules)
	}
}
