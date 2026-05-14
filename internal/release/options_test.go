// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"testing"
)

// TestNewOptions pins the CLI surface: the documented flags
// (-m / --message, --major / --minor / --patch, --bump,
// --version, --dry-run, --no-tag) compose into the [Options]
// shape the rest of the package consumes; the input-validation
// paths return [ErrUsage]-wrapped errors.
func TestNewOptions(t *testing.T) {
	t.Parallel()

	t.Run("message plus force minor produces the matching Options", func(t *testing.T) {
		t.Parallel()
		opts, err := NewOptions("Release notes", false, true, false, nil, "", false, false, false, false, false)
		if err != nil {
			t.Fatalf("NewOptions err: %v", err)
		}
		if opts.Message != "Release notes" {
			t.Fatalf("Message = %q, want %q", opts.Message, "Release notes")
		}
		if opts.Force != BumpMinor {
			t.Fatalf("Force = %v, want BumpMinor", opts.Force)
		}
	})

	t.Run("repeatable --bump records every entry", func(t *testing.T) {
		t.Parallel()
		opts, err := NewOptions("", false, false, false,
			[]string{"cli=minor", "frontend/golang=major"}, "", true, false, false, false, false)
		if err != nil {
			t.Fatalf("NewOptions err: %v", err)
		}
		if got := opts.Overrides["cli"]; got != BumpMinor {
			t.Fatalf("Overrides[cli] = %v, want BumpMinor", got)
		}
		if got := opts.Overrides["frontend/golang"]; got != BumpMajor {
			t.Fatalf("Overrides[frontend/golang] = %v, want BumpMajor", got)
		}
	})

	t.Run("missing message without --dry-run or --no-tag is a usage error", func(t *testing.T) {
		t.Parallel()
		_, err := NewOptions("", true, false, false, nil, "", false, false, false, false, false)
		if !errors.Is(err, ErrUsage) {
			t.Fatalf("NewOptions err = %v, want ErrUsage", err)
		}
	})

	t.Run("more than one force flag is a usage error", func(t *testing.T) {
		t.Parallel()
		_, err := NewOptions("x", true, true, false, nil, "", false, false, false, false, false)
		if !errors.Is(err, ErrUsage) {
			t.Fatalf("NewOptions err = %v, want ErrUsage", err)
		}
	})

	t.Run("malformed --bump value is a usage error", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{"no-equal", "=missingmod", "mod=BOGUS"} {
			_, err := NewOptions("", false, false, false,
				[]string{raw}, "", true, false, false, false, false)
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("NewOptions --bump %q err = %v, want ErrUsage", raw, err)
			}
		}
	})

	t.Run("--dry-run alone is valid (no message required)", func(t *testing.T) {
		t.Parallel()
		opts, err := NewOptions("", false, false, false, nil, "", true, false, false, false, false)
		if err != nil {
			t.Fatalf("NewOptions err: %v", err)
		}
		if !opts.DryRun {
			t.Fatalf("DryRun = false, want true")
		}
	})

	t.Run("--no-tag alone is valid (no message required)", func(t *testing.T) {
		t.Parallel()
		opts, err := NewOptions("", true, false, false, nil, "", false, true, false, false, false)
		if err != nil {
			t.Fatalf("NewOptions err: %v", err)
		}
		if !opts.NoTag {
			t.Fatalf("NoTag = false, want true")
		}
	})

	t.Run("--version vX.Y.Z populates Options.Version", func(t *testing.T) {
		t.Parallel()
		opts, err := NewOptions("Release notes", false, false, false, nil, "v1.0.2", false, false, false, false, false)
		if err != nil {
			t.Fatalf("NewOptions err: %v", err)
		}
		if opts.Version != "v1.0.2" {
			t.Fatalf("Version = %q, want %q", opts.Version, "v1.0.2")
		}
		if opts.Force != BumpNone {
			t.Fatalf("Force = %v, want BumpNone (version path does not set force)", opts.Force)
		}
	})

	t.Run("--version with --major is a usage error", func(t *testing.T) {
		t.Parallel()
		_, err := NewOptions("x", true, false, false, nil, "v1.0.2", false, false, false, false, false)
		if !errors.Is(err, ErrUsage) {
			t.Fatalf("NewOptions err = %v, want ErrUsage (mutually exclusive)", err)
		}
	})

	t.Run("--version with --minor is a usage error", func(t *testing.T) {
		t.Parallel()
		_, err := NewOptions("x", false, true, false, nil, "v1.0.2", false, false, false, false, false)
		if !errors.Is(err, ErrUsage) {
			t.Fatalf("NewOptions err = %v, want ErrUsage (mutually exclusive)", err)
		}
	})

	t.Run("--version with --patch is a usage error", func(t *testing.T) {
		t.Parallel()
		_, err := NewOptions("x", false, false, true, nil, "v1.0.2", false, false, false, false, false)
		if !errors.Is(err, ErrUsage) {
			t.Fatalf("NewOptions err = %v, want ErrUsage (mutually exclusive)", err)
		}
	})

	t.Run("malformed --version is a usage error", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{"1.0.2", "v1.0", "vfoo", "v1.2.x"} {
			_, err := NewOptions("x", false, false, false, nil, raw, false, false, false, false, false)
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("NewOptions --version %q err = %v, want ErrUsage", raw, err)
			}
		}
	})
}
