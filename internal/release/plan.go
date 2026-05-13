// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"fmt"
	"io"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// initialVersion is the version a module reports when no tag
// matching its prefix exists yet. Treated as `0.0.0` so a first-run
// `--major` lands at `1.0.0`, `--minor` at `0.1.0`, `--patch` at
// `0.0.1`. Conventional-commit inference over the module's full
// history applies on the first run too — a `feat!:` commit anywhere
// in the module's history produces `1.0.0` on its first release.
const initialVersion = "0.0.0"

// PlanEntry records the release decision for one module: the
// resolved bump level, the new version, the tag name, and a human
// rationale displayed in the printed plan and in apply-time output.
type PlanEntry struct {
	// Module is the source module the entry describes.
	Module modules.Module

	// OldVersion is the version derived from the module's last
	// matching tag, or [initialVersion] when no prior tag exists.
	OldVersion string

	// NewVersion is the version after applying Level. Equals
	// OldVersion when Level == [BumpNone].
	NewVersion string

	// Level is the bump that produced NewVersion.
	Level BumpLevel

	// Reason is the human-facing rationale for the decision —
	// `--bump override`, `inferred from conventional commits since
	// <tag>`, `no commits in module scope`, etc.
	Reason string

	// Tag is the annotated-tag name to create when applying. Empty
	// when the entry's outcome is "skip" (no commits in scope and
	// no force flag).
	Tag string
}

// Skipped reports whether this entry should produce no tag.
// True when the module had no qualifying commits in scope and no
// force flag overrode the inference.
func (e PlanEntry) Skipped() bool {
	return e.Tag == ""
}

// BuildPlan computes one [PlanEntry] per supplied module and
// returns the full release plan. The current version per module
// derives from the most recent matching git tag (or [initialVersion]
// when no prior tag exists); the bump level applies the precedence
// chain documented on [resolveLevel].
func BuildPlan(
	ctx context.Context, runner xexec.Runner, root string,
	mods []modules.Module, opts Options,
) ([]PlanEntry, error) {
	plan := make([]PlanEntry, 0, len(mods))
	for _, m := range mods {
		entry, err := planEntryFor(ctx, runner, root, m, mods, opts)
		if err != nil {
			return nil, err
		}
		plan = append(plan, entry)
	}
	return plan, nil
}

// planEntryFor builds the [PlanEntry] for one module by reading the
// module's last matching tag (or treating the version as
// [initialVersion] when no prior tag exists) and applying the
// precedence chain in [resolveLevel].
func planEntryFor(
	ctx context.Context, runner xexec.Runner, root string,
	m modules.Module, mods []modules.Module, opts Options,
) (PlanEntry, error) {
	prevTag, err := LastTag(ctx, runner, root, m.TagPrefix())
	if err != nil {
		return PlanEntry{}, fmt.Errorf("look up last tag for %s: %w", m.Dir, err)
	}
	old := initialVersion
	if prevTag != "" {
		v, verr := VersionFromTag(prevTag)
		if verr != nil {
			return PlanEntry{}, fmt.Errorf("parse last tag %q for %s: %w", prevTag, m.Dir, verr)
		}
		old = v
	}

	entry := PlanEntry{Module: m, OldVersion: old}

	level, reason, skipReason, err := resolveLevel(ctx, runner, root, m, mods, opts, prevTag)
	if err != nil {
		return PlanEntry{}, err
	}
	entry.Level = level
	entry.Reason = reason
	if skipReason != "" {
		entry.Reason = skipReason
		entry.NewVersion = old
		return entry, nil
	}

	next, err := BumpSemver(old, level)
	if err != nil {
		return PlanEntry{}, fmt.Errorf("bump %s: %w", m.Dir, err)
	}
	entry.NewVersion = next
	entry.Tag = m.TagPrefix() + "v" + next
	return entry, nil
}

// resolveLevel implements the precedence chain for choosing the
// bump level. Returns (level, reason, skipReason, err): when
// skipReason is non-empty the caller treats the entry as a
// no-action skip.
//
// Precedence (highest first):
//
//  1. `--bump MODULE=LEVEL` per-module override.
//  2. `--major` / `--minor` / `--patch` global force.
//  3. Conventional-commit inference scoped to the module's path
//     (since the prior tag, or against the full history on first
//     run).
func resolveLevel(
	ctx context.Context, runner xexec.Runner, root string,
	m modules.Module, mods []modules.Module, opts Options, prevTag string,
) (BumpLevel, string, string, error) {
	if override, ok := opts.Overrides[m.Dir]; ok {
		if override == BumpNone {
			return BumpNone, "", fmt.Sprintf("--bump %s=none override", m.Dir), nil
		}
		return override, fmt.Sprintf("--bump %s=%s override", m.Dir, override), "", nil
	}
	if opts.Force != BumpNone {
		return opts.Force, fmt.Sprintf("--%s force", opts.Force), "", nil
	}
	commits, err := ScopedCommits(ctx, runner, root, prevTag, includePathsFor(m), excludePathsFor(m, mods))
	if err != nil {
		return BumpNone, "", "", fmt.Errorf("scan commits for %s: %w", m.Dir, err)
	}
	level := ClassifyCommits(commits)
	switch {
	case level == BumpNone && prevTag == "":
		return BumpNone, "", "no commits in module scope (and no prior tag)", nil
	case level == BumpNone:
		return BumpNone, "", fmt.Sprintf("no commits in module scope since %s", prevTag), nil
	case prevTag == "":
		reason := fmt.Sprintf("inferred %s from conventional commits (initial release from history)", level)
		return level, reason, "", nil
	default:
		reason := fmt.Sprintf("inferred %s from conventional commits since %s", level, prevTag)
		return level, reason, "", nil
	}
}

// includePathsFor returns the pathspec include set for m's scoped
// `git log`. The root module's scope is the entire working tree;
// every submodule's scope is its own directory.
func includePathsFor(m modules.Module) []string {
	if m.Dir == "." {
		return []string{"."}
	}
	return []string{m.Dir}
}

// excludePathsFor returns every other module's directory that
// nests under m so the scoped log for m doesn't pick up commits
// owned by those submodules. For the root module that's every
// other entry in the list; for a submodule it's only the entries
// nested below it.
func excludePathsFor(m modules.Module, mods []modules.Module) []string {
	var out []string
	for _, other := range mods {
		if other.Dir == m.Dir {
			continue
		}
		switch {
		case m.Dir == ".":
			out = append(out, other.Dir)
		case strings.HasPrefix(other.Dir+"/", m.Dir+"/"):
			out = append(out, other.Dir)
		}
	}
	return out
}

// printPlan writes a human-readable representation of plan to w.
// Each entry occupies one line; skipped entries are flagged
// explicitly. The first line is the section header.
func printPlan(w io.Writer, plan []PlanEntry) {
	fmt.Fprintln(w, "Release plan:")
	if len(plan) == 0 {
		fmt.Fprintln(w, "  (no modules)")
		return
	}
	for _, e := range plan {
		if e.Skipped() {
			fmt.Fprintf(w, "  %-32s %s -> (skip)        %s\n",
				e.Module.Dir, e.OldVersion, e.Reason)
			continue
		}
		fmt.Fprintf(w, "  %-32s %s -> %-12s tag %-32s (%s)\n",
			e.Module.Dir, e.OldVersion, e.NewVersion, e.Tag, e.Reason)
	}
}

// ApplyPlan creates the annotated tag for every non-skipped entry
// in plan. release makes no file changes and produces no commits —
// every tag points at the current HEAD. The caller is responsible
// for ensuring HEAD reflects the intended release state.
//
// No-op when every entry is skipped.
func ApplyPlan(
	ctx context.Context, runner xexec.Runner, root string, w io.Writer,
	plan []PlanEntry, opts Options,
) error {
	tags := make([]PlanEntry, 0, len(plan))
	for _, e := range plan {
		if !e.Skipped() {
			tags = append(tags, e)
		}
	}
	fmt.Fprintln(w)
	if opts.NoTag {
		fmt.Fprintln(w, "(--no-tag: skipping tag creation)")
		return nil
	}
	if len(tags) == 0 {
		fmt.Fprintln(w, "(nothing to tag)")
		return nil
	}
	fmt.Fprintln(w, "Tagging...")
	for _, e := range tags {
		body := e.Tag + "\n\n" + opts.Message
		if err := Tag(ctx, runner, root, e.Tag, body); err != nil {
			return fmt.Errorf("git tag %s: %w", e.Tag, err)
		}
		fmt.Fprintln(w, "  ", e.Tag)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Done. Tags are local. Push with:")
	fmt.Fprintln(w, "  git push --follow-tags")
	return nil
}
