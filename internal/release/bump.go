// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// BumpLevel is the semantic-version increment kind. Order is
// significant: comparisons (`a > b`) drive the "pick the highest of
// these levels" rule the conventional-commit inference applies.
type BumpLevel int

// BumpLevel values in ascending impact order.
const (
	// BumpNone means leave the version unchanged. Returned by
	// inference when no commits fall inside a module's scope and
	// used as the "skip this module" sentinel.
	BumpNone BumpLevel = iota

	// BumpPatch increments the third semver segment.
	BumpPatch

	// BumpMinor increments the second segment and zeroes the third.
	BumpMinor

	// BumpMajor increments the first segment and zeroes the rest.
	BumpMajor
)

// Canonical textual identifiers — accepted by [ParseBumpLevel] and
// returned by [BumpLevel.String]. Also used in flag descriptions and
// in user-facing reasons on plan entries.
const (
	bumpNoneStr  = "none"
	bumpPatchStr = "patch"
	bumpMinorStr = "minor"
	bumpMajorStr = "major"
)

// String returns the lowercase identifier matching the flag's
// accepted spelling (`none`, `patch`, `minor`, `major`).
func (b BumpLevel) String() string {
	switch b {
	case BumpNone:
		return bumpNoneStr
	case BumpPatch:
		return bumpPatchStr
	case BumpMinor:
		return bumpMinorStr
	case BumpMajor:
		return bumpMajorStr
	default:
		return fmt.Sprintf("BumpLevel(%d)", int(b))
	}
}

// ErrInvalidBumpLevel reports that a textual level isn't recognised.
// Returned by [ParseBumpLevel]; surfaced through ErrUsage at the
// CLI boundary.
var ErrInvalidBumpLevel = errors.New("invalid bump level")

// ParseBumpLevel returns the [BumpLevel] matching the lowercased
// identifier in s. Returns [ErrInvalidBumpLevel] for any other input.
func ParseBumpLevel(s string) (BumpLevel, error) {
	switch s {
	case bumpNoneStr:
		return BumpNone, nil
	case bumpPatchStr:
		return BumpPatch, nil
	case bumpMinorStr:
		return BumpMinor, nil
	case bumpMajorStr:
		return BumpMajor, nil
	default:
		return BumpNone, fmt.Errorf("%w: %q (expected major|minor|patch|none)", ErrInvalidBumpLevel, s)
	}
}

// ErrInvalidSemver reports that a version string doesn't match the
// `X.Y.Z` triplet form the binary supports. Pre-release / build
// metadata suffixes are not handled today.
var ErrInvalidSemver = errors.New("invalid semver")

// semverRegex matches the bare three-segment numeric form.
var semverRegex = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// BumpSemver returns the version that follows old after applying
// level. Returns old unchanged for [BumpNone]. Returns an error
// wrapping [ErrInvalidSemver] when old isn't a bare X.Y.Z triplet.
func BumpSemver(old string, level BumpLevel) (string, error) {
	m := semverRegex.FindStringSubmatch(old)
	if m == nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidSemver, old)
	}
	// Atoi cannot fail: the regex limits the segments to digits.
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	switch level {
	case BumpMajor:
		major++
		minor = 0
		patch = 0
	case BumpMinor:
		minor++
		patch = 0
	case BumpPatch:
		patch++
	case BumpNone:
		// Unchanged.
	default:
		return "", fmt.Errorf("BumpSemver: unsupported level %s", level)
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

// VersionFromTag extracts the X.Y.Z literal from a tag of the form
// `<prefix>v<X>.<Y>.<Z>`. Returns the empty string when tag is empty
// (the no-prior-tag case), and an error wrapping [ErrInvalidSemver]
// when the tag doesn't end with a recognisable v-prefixed semver.
//
// Pre-release / build-metadata suffixes (`v1.0.0-rc.1`,
// `v1.0.0+sha`) are not supported today; such tags surface as
// [ErrInvalidSemver].
func VersionFromTag(tag string) (string, error) {
	if tag == "" {
		return "", nil
	}
	// Strip everything up to and including the last `v` — the tag's
	// prefix (`foo/bar/v1.2.3`) varies per module; only the trailing
	// `vX.Y.Z` portion concerns this parser.
	for i := len(tag) - 1; i >= 0; i-- {
		if tag[i] != 'v' {
			continue
		}
		candidate := tag[i+1:]
		if !semverRegex.MatchString(candidate) {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%w: tag %q has no vX.Y.Z suffix", ErrInvalidSemver, tag)
}
