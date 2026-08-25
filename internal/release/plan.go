// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"fmt"
	"io"
	"slices"
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

	// Pins are the require rewrites this module will receive
	// without being tagged, populated by [annotatePins].
	//
	// A skipped module still depends on siblings that moved, so the
	// release rewrites its requires and commits the result. Without
	// this the plan rendered such a module as `(skip)` while the
	// run wrote to it, and `--dry-run` stopped being a complete
	// description of what a release does.
	//
	// Empty for a tagged module — its rewrites are implied by the
	// tag — and for a skipped module with nothing to change.
	Pins []Pin
}

// Pin is one require rewrite a release will make in a module it
// does not tag.
type Pin struct {
	// Path is the required module's import path.
	Path string

	// From is the version currently in the go.mod.
	From string

	// To is the version the release will pin it to.
	To string
}

// cascadeDependents promotes every module whose intra-workspace
// requires will change into a patch release.
//
// A module skipped for having no commits of its own still has its
// go.mod rewritten when a sibling it requires moves. Committing
// that rewrite without tagging leaves the published module pointing
// at superseded siblings forever: `go get <mod>@latest` returns the
// old tag, whose go.mod still names the versions the workspace has
// left behind. The rewrite is only consumable once it is tagged, so
// a changed dependency is a release.
//
// Patch regardless of the dependency's own level — the dependent's
// API did not change, only what it builds against.
//
// The loop runs to a fixpoint because promotion is transitive: once
// a module gains a new version, everything requiring it has a
// changed require too. Each pass promotes at least one entry or
// stops, so it terminates in at most len(plan) passes.
//
// A no-op under --version, which already puts every module at the
// same version and so leaves nothing skipped to promote.
func cascadeDependents(root string, plan []PlanEntry) ([]PlanEntry, error) {
	// Nothing skipped means nothing to promote, and the graph read
	// below opens every module's go.mod — work a plan that already
	// releases everything cannot use.
	skipped := false
	for _, e := range plan {
		if e.Skipped() {
			skipped = true
			break
		}
	}
	if !skipped {
		return plan, nil
	}

	out := slices.Clone(plan)

	// Fixpoint, because promoting one module moves its version and
	// so can give a module that requires it a pin it did not have a
	// round earlier. Bounded by the module count: each round promotes
	// at least one entry or stops.
	for range len(out) {
		annotated, err := annotatePins(root, out)
		if err != nil {
			return nil, err
		}
		promoted := false
		for i, e := range annotated {
			if !e.Skipped() || len(e.Pins) == 0 {
				continue
			}
			triggers := make([]string, 0, len(e.Pins))
			for _, p := range e.Pins {
				triggers = append(triggers, p.Path)
			}
			slices.Sort(triggers)
			next, bumpErr := BumpSemver(e.OldVersion, BumpPatch)
			if bumpErr != nil {
				return nil, fmt.Errorf("cascade: bump %s: %w", e.Module.Dir, bumpErr)
			}
			out[i].Level = BumpPatch
			out[i].NewVersion = next
			out[i].Tag = e.Module.TagPrefix() + "v" + next
			out[i].Reason = "patch: requires " + strings.Join(triggers, ", ") +
				" — pinned by this release"
			promoted = true
		}
		if !promoted {
			break
		}
	}
	return out, nil
}

// annotatePins fills [PlanEntry.Pins] for every skipped entry by
// reading its go.mod against the finished version map.
//
// Read-only: this runs under --dry-run, where writing anything
// would defeat the point. It shares [pinChanges] with
// [bumpOwnRequires], so what the plan discloses and what the apply
// writes cannot drift apart.
func annotatePins(root string, plan []PlanEntry) ([]PlanEntry, error) {
	// A single module has no siblings to require, so there is
	// nothing to pin and nothing to read.
	if len(plan) < 2 {
		return plan, nil
	}

	versions, _ := planVersions(plan)
	resolved := map[string]bool{}
	for _, e := range plan {
		resolved[e.Module.Dir] = true
	}
	want, err := releasedVersionMap(root, versions, resolved)
	if err != nil {
		return nil, err
	}

	// Every entry, not only the skipped ones: a module being tagged
	// has its requires rewritten too, and showing only half of what
	// a release writes is the same under-reporting in a new place.
	out := make([]PlanEntry, 0, len(plan))
	for _, e := range plan {
		pins, pinErr := pinChanges(root, e.Module.Dir, want)
		if pinErr != nil {
			return nil, pinErr
		}
		e.Pins = pins
		out = append(out, e)
	}
	return out, nil
}

// planWaves groups entries into the order [ApplyPipeline] processes
// them: a module joins a wave once every workspace module it
// requires sits in an earlier one.
//
// The grouping is the execution order and therefore the signing
// order — one commit, N tags and one push per wave, each able to
// block on a hardware key. Printing it lets the operator see how
// many prompts are coming, and for what, before touching the key.
//
// Returns a single wave when the graph is trivial or cyclic, which
// degrades the display rather than failing a release over it;
// [ApplyPipeline] reports a cycle properly.
func planWaves(
	root string, mods []modules.Module, plan []PlanEntry,
) ([][]PlanEntry, error) {
	if len(plan) < 2 {
		return [][]PlanEntry{plan}, nil
	}
	deps, err := workspaceDeps(root, mods)
	if err != nil {
		return nil, err
	}

	placed := map[string]bool{}
	var waves [][]PlanEntry
	for len(placed) < len(plan) {
		var wave []PlanEntry
		for _, e := range plan {
			if placed[e.Module.Dir] {
				continue
			}
			ready := true
			for dep := range deps[e.Module.Dir] {
				if !placed[dep] {
					ready = false
					break
				}
			}
			if ready {
				wave = append(wave, e)
			}
		}
		if len(wave) == 0 {
			// Every remaining module is waiting on another that is
			// also waiting: a cycle. Reported rather than quietly
			// flattened — the ungrouped plan was the only signal an
			// operator got, and it reads as a formatting choice, not
			// as "this release cannot be ordered and will abort".
			var stuck []string
			for _, e := range plan {
				if !placed[e.Module.Dir] {
					stuck = append(stuck, e.Module.Dir)
				}
			}
			return [][]PlanEntry{plan}, &CycleError{Modules: stuck}
		}
		for _, e := range wave {
			placed[e.Module.Dir] = true
		}
		waves = append(waves, wave)
	}
	return waves, nil
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

	// --version pins every non-skipped module at the supplied
	// version, bypassing bump-level resolution entirely. The only
	// per-module input still consulted is `--bump MODULE=none`,
	// which lets the caller skip a specific module from the
	// coordinated release.
	if opts.Version != "" {
		if override, ok := opts.Overrides[m.Dir]; ok && override == BumpNone {
			entry.Reason = fmt.Sprintf("--bump %s=none override", m.Dir)
			entry.NewVersion = old
			return entry, nil
		}
		forced, vErr := VersionFromTag(opts.Version)
		if vErr != nil {
			return PlanEntry{}, fmt.Errorf("parse --version %q: %w", opts.Version, vErr)
		}
		entry.NewVersion = forced
		entry.Tag = m.TagPrefix() + "v" + forced
		entry.Reason = fmt.Sprintf("--version v%s pin", forced)
		return entry, nil
	}

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

// printPlan writes a human-readable representation of waves to w.
//
// Each entry occupies one line — module, old → new, tag, and the
// reason the level was chosen — followed by one indented line per
// require the release will rewrite. Skipped entries are flagged.
//
// Wave headings and the signing summary appear only when there is
// more than one wave, so a single-module repository is not made to
// read like a fleet. The summary exists because this output is what
// an operator reads immediately before a hardware key starts asking
// for a PIN: it answers how many prompts are coming and why, which
// a flat list of ten modules does not.
func printPlan(w io.Writer, waves [][]PlanEntry) {
	total, tags, pins, commits := 0, 0, 0, 0
	for _, wave := range waves {
		writes := false
		for _, e := range wave {
			total++
			if !e.Skipped() {
				tags++
			}
			pins += len(e.Pins)
			if len(e.Pins) > 0 {
				writes = true
			}
		}
		if writes {
			commits++
		}
	}
	if total == 0 {
		fmt.Fprintln(w, "Release plan:")
		fmt.Fprintln(w, "  (no modules)")
		return
	}

	// Wave headings earn their space only when there is more than
	// one; a single-module repository keeps the flat listing.
	grouped := len(waves) > 1
	indent := "  "
	if grouped {
		fmt.Fprintf(w, "Release plan — %d modules in %d waves, %d tags\n",
			total, len(waves), tags)
		indent = "    "
	} else {
		fmt.Fprintln(w, "Release plan:")
	}

	for i, wave := range waves {
		if grouped {
			after := "nothing published yet"
			if i > 0 {
				after = fmt.Sprintf("after wave %d is pushed", i)
			}
			fmt.Fprintf(w, "\n  wave %d  ·  %s\n", i+1, after)
		}
		for _, e := range wave {
			target, action := e.NewVersion, "tag "+e.Tag
			if e.Skipped() {
				target, action = "(skip)", ""
			}
			fmt.Fprintf(w, "%s%-26s %-8s -> %-10s %-34s (%s)\n",
				indent, e.Module.Dir, e.OldVersion, target, action, e.Reason)
			for _, p := range e.Pins {
				fmt.Fprintf(w, "%s    pin  %-42s %s -> %s\n",
					indent, p.Path, p.From, p.To)
			}
		}
	}

	if grouped {
		fmt.Fprintf(w,
			"\n  Each wave: tag → push → pin dependents → tidy → one commit.\n"+
				"  %d pin(s); %d tag, %d commit and %d push signature(s).\n",
			pins, tags, commits, len(waves))
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
		// EnsureTag is idempotent: a tag left from a prior partial
		// run that already points at HEAD is silently preserved
		// rather than re-created.
		if err := EnsureTag(ctx, runner, root, e.Tag, body); err != nil {
			return fmt.Errorf("git tag %s: %w", e.Tag, err)
		}
		fmt.Fprintln(w, "  ", e.Tag)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Done. Tags are local. Push with:")
	fmt.Fprintln(w, "  git push --follow-tags")
	return nil
}

// CycleError reports that the workspace's modules require each other
// in a loop, so no release order exists.
//
// Not fatal to the plan: [printPlan] still lists every module, and
// the operator needs that listing to see what the release would do.
// It is fatal to the layered pipeline, which advances by releasing
// modules whose dependencies are already released and therefore
// never starts. Surfacing it at plan time turns an abort part-way
// through a signing session into a warning before the first prompt.
//
// Go permits a module cycle and the toolchain resolves it, so this
// is not a defect the workspace has to fix — but a release either
// breaks the cycle or gives up ordering entirely, which is what
// --version does.
type CycleError struct {
	// Modules are the directories that could not be ordered, in
	// plan order.
	Modules []string
}

func (e *CycleError) Error() string {
	return "release: cyclic intra-workspace dependency among " +
		strings.Join(e.Modules, ", ") +
		"; no release order exists — break the cycle, or pass " +
		"--version to release every module at one version in a single wave"
}
