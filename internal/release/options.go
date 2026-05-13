// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUsage signals a CLI-input fault — a malformed flag value, a
// conflicting combination, or a missing required flag. The cobra
// layer surfaces it to the user; ergon's `main` does not special-case
// it for an exit code today.
var ErrUsage = errors.New("usage")

// Options carries the per-invocation choices `ergon release` reads
// from cobra flags. The struct is constructed from raw flag values
// by [NewOptions], which performs the validation that used to live
// inside the standalone parseFlags.
type Options struct {
	// Message is the annotated-tag body release appends after each
	// tag's own name. Required unless DryRun or NoTag is set.
	Message string

	// Force is the bump level forced on every module. BumpNone
	// means no force; per-module Overrides still apply.
	Force BumpLevel

	// Overrides maps a module directory (`.` for root, relative
	// path for submodules) to the bump level to apply, taking
	// precedence over Force and over conventional-commit inference.
	Overrides map[string]BumpLevel

	// DryRun prints the plan without changing files or creating tags.
	DryRun bool

	// NoTag prints the plan but skips tag creation. Useful for
	// inspecting the resolution before tagging.
	NoTag bool
}

// NewOptions builds an [Options] from the raw cobra flag values.
// Returns an [ErrUsage]-wrapped error when the inputs are invalid
// (mutually-exclusive force flags, malformed --bump entry, missing
// required message).
func NewOptions(
	message string, forceMajor, forceMinor, forcePatch bool,
	bumps []string, dryRun, noTag bool,
) (Options, error) {
	opts := Options{
		Message: message,
		DryRun:  dryRun,
		NoTag:   noTag,
	}

	level, err := pickForce(forceMajor, forceMinor, forcePatch)
	if err != nil {
		return Options{}, err
	}
	opts.Force = level

	if len(bumps) > 0 {
		opts.Overrides = make(map[string]BumpLevel, len(bumps))
		for _, raw := range bumps {
			mod, levelStr, ok := strings.Cut(raw, "=")
			if !ok || mod == "" {
				return Options{}, fmt.Errorf("%w: --bump expects MODULE=LEVEL, got %q", ErrUsage, raw)
			}
			bl, err := ParseBumpLevel(levelStr)
			if err != nil {
				return Options{}, fmt.Errorf("%w: --bump %s: %w", ErrUsage, raw, err)
			}
			opts.Overrides[mod] = bl
		}
	}

	if !dryRun && !noTag && message == "" {
		return Options{}, fmt.Errorf("%w: -m/--message is required (or pass --dry-run / --no-tag)", ErrUsage)
	}
	return opts, nil
}

// pickForce reduces the three boolean force-flags to a single
// [BumpLevel]. Mutually exclusive: passing more than one returns
// an [ErrUsage]-wrapped error.
func pickForce(major, minor, patch bool) (BumpLevel, error) {
	count := 0
	level := BumpNone
	if major {
		count++
		level = BumpMajor
	}
	if minor {
		count++
		level = BumpMinor
	}
	if patch {
		count++
		level = BumpPatch
	}
	if count > 1 {
		return BumpNone, fmt.Errorf("%w: --major / --minor / --patch are mutually exclusive", ErrUsage)
	}
	return level, nil
}
