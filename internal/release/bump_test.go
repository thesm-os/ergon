// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"testing"
)

// TestParseBumpLevel covers the textual-form round-trip plus the
// invalid-input sentinel surface — the validator that backs both
// the `-bump MOD=LEVEL` flag parser and any future direct call.
func TestParseBumpLevel(t *testing.T) {
	t.Parallel()

	t.Run("valid forms map to the matching constant", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			in   string
			want BumpLevel
		}{
			{"none", BumpNone},
			{"patch", BumpPatch},
			{"minor", BumpMinor},
			{"major", BumpMajor},
		} {
			got, err := ParseBumpLevel(tc.in)
			if err != nil {
				t.Fatalf("ParseBumpLevel(%q) returned err: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseBumpLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
		}
	})

	t.Run("unknown form surfaces ErrInvalidBumpLevel", func(t *testing.T) {
		t.Parallel()
		_, err := ParseBumpLevel("MAJOR")
		if !errors.Is(err, ErrInvalidBumpLevel) {
			t.Fatalf("ParseBumpLevel(MAJOR) err = %v, want ErrInvalidBumpLevel", err)
		}
	})
}

// TestBumpSemver pins the per-level increment behaviour plus the
// invalid-input rejection. Table-driven across the four supported
// levels; an unparseable input is exercised separately.
func TestBumpSemver(t *testing.T) {
	t.Parallel()

	t.Run("each level increments the documented segment", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			old   string
			level BumpLevel
			want  string
		}{
			{"1.2.3", BumpMajor, "2.0.0"},
			{"1.2.3", BumpMinor, "1.3.0"},
			{"1.2.3", BumpPatch, "1.2.4"},
			{"1.2.3", BumpNone, "1.2.3"},
			{"0.0.0", BumpMajor, "1.0.0"},
			{"0.0.0", BumpMinor, "0.1.0"},
			{"0.0.0", BumpPatch, "0.0.1"},
			{"9.9.9", BumpMinor, "9.10.0"},
		} {
			got, err := BumpSemver(tc.old, tc.level)
			if err != nil {
				t.Fatalf("BumpSemver(%q, %v) err: %v", tc.old, tc.level, err)
			}
			if got != tc.want {
				t.Fatalf("BumpSemver(%q, %v) = %q, want %q", tc.old, tc.level, got, tc.want)
			}
		}
	})

	t.Run("non-X.Y.Z input surfaces ErrInvalidSemver", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{"", "1.0", "1.0.0-rc1", "v1.0.0", "1.0.0.0"} {
			_, err := BumpSemver(in, BumpPatch)
			if !errors.Is(err, ErrInvalidSemver) {
				t.Fatalf("BumpSemver(%q) err = %v, want ErrInvalidSemver", in, err)
			}
		}
	})
}

// TestVersionFromTag pins the tag → version parser used to derive
// the "current version" of a module from its most recent matching
// tag. Per the Go multi-module convention the tag prefix varies
// across modules; the parser only looks for the trailing
// `vX.Y.Z` suffix.
func TestVersionFromTag(t *testing.T) {
	t.Parallel()

	t.Run("recognised forms return the bare semver", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			tag  string
			want string
		}{
			{"v1.2.3", "1.2.3"},
			{"v0.0.1", "0.0.1"},
			{"frontend/golang/v2.0.0", "2.0.0"},
			{"deeply/nested/path/v10.0.0", "10.0.0"},
			{"eidostest/v0.1.0", "0.1.0"},
		} {
			got, err := VersionFromTag(tc.tag)
			if err != nil {
				t.Fatalf("VersionFromTag(%q) err: %v", tc.tag, err)
			}
			if got != tc.want {
				t.Fatalf("VersionFromTag(%q) = %q, want %q", tc.tag, got, tc.want)
			}
		}
	})

	t.Run("empty tag yields empty version with no error", func(t *testing.T) {
		t.Parallel()
		got, err := VersionFromTag("")
		if err != nil {
			t.Fatalf("VersionFromTag(empty) err: %v", err)
		}
		if got != "" {
			t.Fatalf("VersionFromTag(empty) = %q, want empty", got)
		}
	})

	t.Run("malformed tag surfaces ErrInvalidSemver", func(t *testing.T) {
		t.Parallel()
		for _, tag := range []string{"foo", "v1", "v1.0", "release-1.0.0", "v1.0.0-rc1"} {
			_, err := VersionFromTag(tag)
			if !errors.Is(err, ErrInvalidSemver) {
				t.Fatalf("VersionFromTag(%q) err = %v, want ErrInvalidSemver", tag, err)
			}
		}
	})
}
