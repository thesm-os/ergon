// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
)

// ApplyPipeline drives the multi-module release end-to-end:
//
// For each topological layer (innermost dependencies first):
//
//  1. Tag every module in this layer at HEAD.
//  2. `git push --follow-tags`, publishing those versions.
//  3. Rewrite the go.mod of every module that directly requires one
//     of them, pinning to the version just published.
//  4. Run `go mod tidy` in those modules to refresh go.sum — step 2
//     put the versions on the remote, so they resolve.
//  5. Commit the propagation as a single `chore(release):` commit.
//
// Propagation follows the push rather than preceding the tag, and
// that ordering is the whole design. Pinning first would require
// versions no tag has created yet, which is how a release died
// against `unknown revision backend/golang/v1.7.0`. Pinning only
// this layer's modules would leave every other module holding a
// superseded require at the commit the next layer tags — and because
// a locally replaced sibling is read off disk, they disagree the
// instant one module moves, which is how seven modules came to fail
// `go mod tidy` at an already-published tag.
//
// The invariant the ordering buys: a layer's commit is the commit
// the next layer tags, and it is internally consistent. The final
// layer is the leaf set — nothing requires it, so it propagates
// nothing and leaves no commit dangling past the last push.
//
// Options that shape the pipeline:
//
//   - NoTag: print the plan, do nothing else.
//   - NoBump: skip steps 3–5; tag every layer without rewriting
//     go.mods. Independent of NoPush — setting one does not set the
//     other.
//   - NoPush: also skip the per-layer pushes; the release stays
//     local (useful for offline rehearsals).
//   - AllowDirty: bypass the working-tree cleanliness check.
func ApplyPipeline(
	ctx context.Context, runner xexec.Runner, root string, w io.Writer,
	mods []modules.Module, plan []PlanEntry, opts Options,
) error {
	if opts.NoTag {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "(--no-tag: skipping tag creation)")
		return nil
	}
	if !opts.AllowDirty {
		dirty, err := HEADIsDirty(ctx, runner, root)
		if err != nil {
			return fmt.Errorf("release: check working tree: %w", err)
		}
		if dirty {
			if !opts.Resume {
				return fmt.Errorf(
					"release: working tree has uncommitted changes; commit or stash " +
						"them, pass --resume to continue an interrupted release, or " +
						"--allow-dirty to bypass this check entirely",
				)
			}
			stray, strayErr := dirtOutsideModFiles(ctx, runner, root, mods)
			if strayErr != nil {
				return strayErr
			}
			if len(stray) > 0 {
				return fmt.Errorf(
					"release: --resume permits only uncommitted go.mod and go.sum "+
						"files left by an interrupted run, but these also differ: %s; "+
						"commit or stash them",
					strings.Join(stray, ", "),
				)
			}
		}
	}

	// Pin every non-skipped entry's new version so later layers
	// can rewrite their require lines against a stable map.
	//
	// Skipped entries seed the map too, at the version they are
	// already released at. A module is skipped because its content
	// is already tagged — either a prior run released it, or it had
	// no commits in scope — so a dependent released in this run
	// must pin that tag rather than whatever older version its
	// go.mod happens to carry. Omitting them made a resumed release
	// write incomplete rewrites: eidos's frontend/* tags pin
	// eidos v1.3.0 although v1.3.1 had been tagged minutes earlier
	// by the run that died. MVS resolves it, but the tagged go.mod
	// misreports what the module was built against.
	//
	// toTag counts only the entries that will produce a tag: the
	// map is no longer a proxy for "is there anything to do", since
	// an all-skipped plan now populates it.
	versions, toTag := planVersions(plan)
	if toTag == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "(nothing to tag)")
		return nil
	}

	deps, err := workspaceDeps(root, mods)
	if err != nil {
		return err
	}
	ownPaths, err := workspaceModulePaths(root, mods)
	if err != nil {
		return err
	}

	// Skipped entries are considered already-resolved so they do
	// not block dependents from advancing through the topology.
	released := map[string]bool{}
	for _, e := range plan {
		if e.Skipped() {
			released[e.Module.Dir] = true
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Tagging...")
	// Tracks a propagation commit still awaiting a push; see the
	// flush after the loop.
	pending := false
	// Modules whose version has already been written into their
	// dependents, so no layer repeats another's work.
	propagated := map[string]bool{}
	// Each layer is headed so the signing prompts below it are
	// attributable: a ten-module release otherwise reads as an
	// undifferentiated run of PIN prompts.
	layer := 0

	// propagate writes every released-but-not-yet-propagated version
	// into the modules that require it, then commits.
	//
	// Runs BEFORE the layer's own tags rather than after, so HEAD is
	// already consistent when a tag is placed on it. In a clean run
	// the emitted order is identical either way — a layer's sources
	// are the previous layer's tags. It matters on a resume: a run
	// interrupted between its tag and its commit leaves that
	// propagation owed, and the re-run's plan reports the tagged
	// module as skipped, so it is seeded as released and never
	// appears in a layer. Propagating at the bottom would tag the
	// next layer onto the tree the interrupted run failed to finish.
	//
	// Gated on NoBump rather than only the tidy and commit that
	// follow: bumpOwnRequires writes go.mod files to disk, so calling
	// it under --no-bump left the user with modified go.mods that
	// were never committed while the tags pointed at commits without
	// them. The flag means "do not touch my go.mods", so it has to
	// skip the write too.
	propagate := func() error {
		if opts.NoBump {
			return nil
		}
		var sources []string
		for dir := range released {
			if !propagated[dir] {
				sources = append(sources, dir)
				propagated[dir] = true
			}
		}
		if len(sources) == 0 {
			return nil
		}
		// deps and released are maps, so both this list and the
		// dependents derived from it are sorted for a stable
		// announcement.
		sort.Strings(sources)
		dependents := directDependents(deps, sources)
		if len(dependents) == 0 {
			return nil
		}
		// Snapshotted before either write, and over go.sum whether or
		// not it exists yet, so a checksum file tidy creates from
		// nothing still registers as a change.
		candidates := modPaths(dependents)
		before := snapshotPaths(root, candidates)
		if _, err := bumpOwnRequires(root, dependents, versions, released); err != nil {
			return err
		}
		// Tidy every dependent, not only those whose go.mod was
		// rewritten. A module can need go.sum entries without needing
		// a require bump: it reaches the new version transitively, or
		// a locally replaced sibling's own requirements shifted under
		// it. Gating tidy on "go.mod changed" left those go.sums
		// stale in the tagged tree.
		//
		// Gated on push-mode because tidy resolves versions the push
		// published. With --no-push those tags exist only locally, so
		// go.sum stays stale until the user tidies after pushing.
		if !opts.NoPush {
			if err := tidyModules(ctx, runner, root, dependents, ownPaths); err != nil {
				return err
			}
		}
		// What to commit is decided by what actually differs on disk,
		// not by what the bumper predicted: tidy edits go.sum behind
		// the bumper's back, and a layer whose only effect was a
		// checksum refresh must still be committed. Compared directly
		// rather than asked of git, so the decision does not depend
		// on a subprocess.
		changed := changedSince(root, candidates, before)
		if len(changed) == 0 {
			return nil
		}
		names := propagatedNames(sources, plan)
		fmt.Fprintf(w, "  commit  %s  (%s)\n",
			pinCommitSubject, strings.Join(dirsOf(changed), ", "))
		if err := commitPaths(
			ctx, runner, root, changed, PinCommitMessage(names)); err != nil {
			return layerFailure(sources, plan, "commit", err)
		}
		pending = true
		return nil
	}

	for {
		ready := layerReady(plan, deps, released)
		if len(ready) == 0 {
			break
		}
		layer++
		fmt.Fprintf(w, "\nlayer %d — %s\n", layer, strings.Join(tagNames(ready, plan), ", "))

		// Steps 3-5 of the PREVIOUS layer, settled before this one
		// tags anything.
		if err := propagate(); err != nil {
			return err
		}

		// Step 1: tag at HEAD, which is the previous layer's
		// propagation commit.
		//
		// EnsureTag is idempotent — a tag left behind by a prior
		// partial run that targeted the same version is preserved
		// (when it points at HEAD) so retrying does not re-tag or
		// move the existing tag.
		//
		// Announced before each call, not after. Signing blocks on a
		// hardware-key PIN prompt, and a prompt with nothing above it
		// naming the operation asks the operator to authorise
		// something they cannot see.
		for _, dir := range ready {
			entry := planEntry(plan, dir)
			body := entry.Tag + "\n\n" + opts.Message
			fmt.Fprintf(w, "  tag     %s\n", entry.Tag)
			if err := EnsureTag(ctx, runner, root, entry.Tag, body); err != nil {
				return fmt.Errorf("git tag %s: %w", entry.Tag, err)
			}
			released[dir] = true
		}

		// Step 2: publish, so the propagation below resolves these
		// versions from a remote that actually has them.
		if !opts.NoPush {
			fmt.Fprintf(w, "  push    %s\n", strings.Join(tagNames(ready, plan), ", "))
			if err := pushFollowTags(ctx, runner, root); err != nil {
				return layerFailure(ready, plan, "push", err)
			}
			pending = false
		}
	}

	// The final layer is the leaf set — nothing requires it, so this
	// normally finds nothing. --no-cascade breaks that: a dependent
	// the cascade would have promoted stays skipped, so it is written
	// to but never tagged.
	if err := propagate(); err != nil {
		return err
	}

	// A propagation commit is otherwise published by the next layer's
	// push; a trailing one has no later push to carry it.
	if pending && !opts.NoPush {
		fmt.Fprintln(w, "\n  push    trailing propagation commit")
		if err := pushFollowTags(ctx, runner, root); err != nil {
			return fmt.Errorf("release: push trailing propagation commit: %w", err)
		}
	}

	// Sanity check: every non-skipped entry should be released.
	for _, e := range plan {
		if !e.Skipped() && !released[e.Module.Dir] {
			return fmt.Errorf(
				"release: %s could not be tagged — cyclic intra-workspace dependency?",
				e.Module.Dir,
			)
		}
	}

	fmt.Fprintln(w)
	if opts.NoPush {
		fmt.Fprintln(w, "Done. Tags are local. Push with:")
		fmt.Fprintln(w, "  git push --follow-tags")
	} else {
		fmt.Fprintln(w, "Done. Every layer's tags + bump commit are already pushed.")
	}
	return nil
}

// pushFollowTags publishes the current branch's unpushed commits
// plus every annotated tag reachable from those commits. Used at
// the end of each layer so the just-created tags become resolvable
// for the next layer's `go mod tidy`.
//
// --no-verify for the reason given on [commitPaths]: a pre-push
// hook running the workspace-wide gate cannot pass mid-pipeline.
func pushFollowTags(ctx context.Context, runner xexec.Runner, root string) error {
	var buf bytes.Buffer
	err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &buf, Stderr: &buf},
		"git", "push", "--no-verify", "--follow-tags")
	if err != nil {
		return fmt.Errorf("git push: %w: %s", err, strings.TrimSpace(buf.String()))
	}
	return nil
}

// tidyModules runs `go mod tidy` in each module directory so the
// rewritten require lines reach go.sum. Called only after the
// prior-layer tags have been pushed; otherwise tidy fails to
// resolve them through the proxy or direct git fetch.
func tidyModules(
	ctx context.Context, runner xexec.Runner, root string,
	dirs, ownPaths []string,
) error {
	env := directFetchEnv(ownPaths)
	for _, d := range dirs {
		var buf bytes.Buffer
		err := runner.Run(ctx,
			xexec.Options{Dir: filepath.Join(root, d), Env: env, Stdout: &buf, Stderr: &buf},
			"go", "mod", "tidy")
		if err != nil {
			return fmt.Errorf("go mod tidy in %s: %w: %s",
				d, err, strings.TrimSpace(buf.String()))
		}
	}
	return nil
}

// workspaceModulePaths returns the import path of every module in
// the workspace, in module order.
func workspaceModulePaths(root string, mods []modules.Module) ([]string, error) {
	out := make([]string, 0, len(mods))
	for _, m := range mods {
		ip, err := readModulePath(filepath.Join(root, m.Dir, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("workspaceModulePaths: %s: %w", m.Dir, err)
		}
		out = append(out, ip)
	}
	return out, nil
}

// directFetchEnv makes `go mod tidy` resolve the workspace's own
// modules straight from their VCS origin, bypassing the module proxy
// and the checksum database for those paths only.
//
// The release pushes a tag and then, moments later, asks the Go
// toolchain to resolve it. Routed through proxy.golang.org that is a
// race against a third-party cache, and a lost one is not merely a
// delay: the proxy caches negative lookups, so an earlier attempt
// that asked for the version before it existed — precisely what a
// release that failed partway through does — leaves an "unknown
// revision" entry that outlives the push. An eidos release died this
// way on backend/golang@v1.7.0 while its same-second sibling
// frontend/protobuf@v1.5.2 resolved fine; the difference was that
// only the former had been requested by the previous failed run.
//
// Retrying would work eventually, since the entry does expire, but
// its lifetime belongs to a service ergon does not operate. Fetching
// from the origin removes the dependency rather than waiting on it:
// git has the tag, because ergon pushed it there and the push
// returned success before this ran.
//
// Scoped to the workspace's own paths. Third-party dependencies keep
// proxy caching and checksum-database verification, which is what
// makes skipping verification here defensible: these modules are
// ones this very process just created and pushed.
//
// Returns nil for an empty list so the child environment is left
// untouched rather than being handed empty overrides.
func directFetchEnv(ownPaths []string) []string {
	if len(ownPaths) == 0 {
		return nil
	}
	own := strings.Join(ownPaths, ",")
	out := make([]string, 0, 2)
	// Prepended to any value already set rather than replacing it: an
	// operator with private modules of their own has GONOPROXY
	// configured for them, and clobbering it would silently route
	// their private code through the public proxy.
	for _, key := range []string{"GONOPROXY", "GONOSUMDB"} {
		value := own
		if prior := os.Getenv(key); prior != "" {
			value = prior + "," + own
		}
		out = append(out, key+"="+value)
	}
	return out
}

// HEADIsDirty reports whether the working tree at root has
// uncommitted changes per `git status --porcelain`. Any non-empty
// output means dirty. Returns the wrapped git error when the
// command itself fails (e.g. not a git repository).
func HEADIsDirty(ctx context.Context, runner xexec.Runner, root string) (bool, error) {
	var buf bytes.Buffer
	err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &buf, Stderr: &buf},
		"git", "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status: %w: %s", err, strings.TrimSpace(buf.String()))
	}
	return strings.TrimSpace(buf.String()) != "", nil
}

// workspaceDeps returns, for each module, the set of OTHER
// workspace modules it `require`s in its go.mod. The set is keyed
// by the dependency's module directory so callers can compose it
// with the [PlanEntry] map without re-reading go.mod files.
func workspaceDeps(root string, mods []modules.Module) (map[string]map[string]bool, error) {
	importToDir := map[string]string{}
	for _, m := range mods {
		ip, err := readModulePath(filepath.Join(root, m.Dir, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("workspaceDeps: %s: %w", m.Dir, err)
		}
		importToDir[ip] = m.Dir
	}

	out := map[string]map[string]bool{}
	for _, m := range mods {
		out[m.Dir] = map[string]bool{}
		body, err := os.ReadFile(filepath.Join(root, m.Dir, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("workspaceDeps: read %s: %w", m.Dir, err)
		}
		f, err := modfile.Parse(filepath.Join(m.Dir, "go.mod"), body, nil)
		if err != nil {
			return nil, fmt.Errorf("workspaceDeps: parse %s: %w", m.Dir, err)
		}
		for _, r := range f.Require {
			if r == nil {
				continue
			}
			dir, ok := importToDir[r.Mod.Path]
			if !ok || dir == m.Dir {
				continue
			}
			out[m.Dir][dir] = true
		}
	}
	return out, nil
}

// modPaths returns the repo-relative go.mod and go.sum path of each
// directory, whether or not either file exists yet.
func modPaths(dirs []string) []string {
	out := make([]string, 0, len(dirs)*2)
	for _, d := range dirs {
		out = append(out, filepath.Join(d, "go.mod"), filepath.Join(d, "go.sum"))
	}
	return out
}

// snapshotPaths records the current bytes of each path. A path that
// does not exist is recorded absent, so its later creation reads as
// a change rather than as no-op.
func snapshotPaths(root string, paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		// A read failure is treated as absence on purpose: the only
		// consequence is that the path is considered changed and
		// gets committed, which is the safe direction.
		if body, err := os.ReadFile(filepath.Join(root, p)); err == nil {
			out[p] = string(body)
		}
	}
	return out
}

// changedSince returns the paths whose contents differ from before,
// skipping any that still do not exist.
func changedSince(root string, paths []string, before map[string]string) []string {
	var out []string
	for _, p := range paths {
		body, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			continue
		}
		prior, existed := before[p]
		if !existed || prior != string(body) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// dirsOf reduces a list of go.mod / go.sum paths to the sorted set
// of module directories holding them, for the commit announcement.
func dirsOf(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		d := filepath.ToSlash(filepath.Dir(p))
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// dirtOutsideModFiles returns the uncommitted paths that are not a
// workspace module's go.mod or go.sum.
//
// This is what makes --resume narrower than --allow-dirty. An
// interrupted release leaves exactly those two files edited, in
// modules it was mid-propagation on, so permitting them lets the
// release be picked up where it stopped. Permitting anything else
// would let unrelated working-tree edits ride into a tagged tree,
// which is the accident the cleanliness check exists to prevent and
// which cannot be undone once the proxy caches the tag.
func dirtOutsideModFiles(
	ctx context.Context, runner xexec.Runner, root string, mods []modules.Module,
) ([]string, error) {
	allowed := make(map[string]bool, len(mods)*2)
	for _, m := range mods {
		for _, p := range modPaths([]string{m.Dir}) {
			// Compared against git's output, which is always
			// slash-separated regardless of platform.
			allowed[filepath.ToSlash(p)] = true
		}
	}

	var buf bytes.Buffer
	if err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &buf, Stderr: &buf},
		"git", "status", "--porcelain"); err != nil {
		return nil, fmt.Errorf("release: git status: %w: %s",
			err, strings.TrimSpace(buf.String()))
	}
	var stray []string
	for line := range strings.SplitSeq(buf.String(), "\n") {
		// Porcelain v1: two status columns, a space, then the path.
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		if !allowed[p] {
			stray = append(stray, p)
		}
	}
	sort.Strings(stray)
	return stray, nil
}

// propagatedNames returns the tag naming each source module's
// current version, for the propagation commit's body.
//
// Falls back to reconstructing the tag from OldVersion when the plan
// carries none: on a resumed release the module was tagged by the
// interrupted run, so this run reports it as skipped with an empty
// Tag — but its version is exactly what is being propagated, and a
// commit body listing nothing would describe the wrong change.
func propagatedNames(sources []string, plan []PlanEntry) []string {
	out := make([]string, 0, len(sources))
	for _, dir := range sources {
		for _, e := range plan {
			if e.Module.Dir != dir {
				continue
			}
			if e.Tag != "" {
				out = append(out, e.Tag)
			} else if e.OldVersion != "" && e.OldVersion != initialVersion {
				out = append(out, e.Module.TagPrefix()+"v"+e.OldVersion)
			}
			break
		}
	}
	return out
}

// directDependents returns the workspace modules whose go.mod
// directly requires at least one module in tagged.
//
// Direct is enough. A module that reaches a tagged one only through
// an intermediary is unaffected until that intermediary is itself
// tagged, which happens in a later layer and propagates from there.
//
// The result deliberately includes modules the plan skips: a skipped
// module still requires its siblings, and leaving it un-propagated is
// exactly what makes the workspace inconsistent at the next layer's
// tag.
func directDependents(deps map[string]map[string]bool, tagged []string) []string {
	want := make(map[string]bool, len(tagged))
	for _, d := range tagged {
		want[d] = true
	}
	var out []string
	for dir, ds := range deps {
		for dep := range ds {
			if want[dep] {
				out = append(out, dir)
				break
			}
		}
	}
	// deps is a map and Go randomises its iteration order; sorted so
	// the commit's file list and the announcement above it read the
	// same on every run.
	sort.Strings(out)
	return out
}

// layerReady returns the modules in plan whose intra-workspace
// dependencies have all already been released (or are skipped).
// Sorts the result so the visible per-layer order is deterministic
// and matches the plan order; the caller can pass it straight
// into Tag without secondary sorting.
func layerReady(plan []PlanEntry, deps map[string]map[string]bool, released map[string]bool) []string {
	var ready []string
	for _, e := range plan {
		if released[e.Module.Dir] || e.Skipped() {
			continue
		}
		blocked := false
		for dep := range deps[e.Module.Dir] {
			if !released[dep] {
				blocked = true
				break
			}
		}
		if !blocked {
			ready = append(ready, e.Module.Dir)
		}
	}
	sort.Strings(ready)
	return ready
}

// bumpOwnRequires rewrites the go.mod files of the modules in
// `ready` so each require line pointing at an already-released
// workspace module pins to that module's just-tagged version.
// Returns the directories of every module whose go.mod was
// actually changed.
//
// "Bottom-up" relative to [ApplyPipeline]'s topology: each layer
// brings its OWN go.mod up to date against the just-published
// prior layers, then tags itself. Contrast with the alternative
// "push from leaves" model, where tagging a leaf retroactively
// updates every dependent — that flow stalls because tidy can't
// resolve the leaf's tag until it's pushed, and pushing requires
// a tag-commit that already has the bumped deps.
func bumpOwnRequires(
	root string, ready []string, versions map[string]string, released map[string]bool,
) ([]string, error) {
	releasedVersions, err := releasedVersionMap(root, versions, released)
	if err != nil {
		return nil, err
	}
	if len(releasedVersions) == 0 {
		return nil, nil
	}

	bumped := []string{}
	for _, dir := range ready {
		// The same detection the plan discloses through
		// [annotatePins], so what is announced and what is written
		// cannot diverge.
		pins, pinErr := pinChanges(root, dir, releasedVersions)
		if pinErr != nil {
			return nil, pinErr
		}
		if len(pins) == 0 {
			continue
		}
		modPath := filepath.Join(root, dir, "go.mod")
		body, err := os.ReadFile(modPath)
		if err != nil {
			return nil, fmt.Errorf("bumpOwnRequires: read %s: %w", dir, err)
		}
		f, err := modfile.Parse(modPath, body, nil)
		if err != nil {
			return nil, fmt.Errorf("bumpOwnRequires: parse %s: %w", dir, err)
		}
		for _, p := range pins {
			if addErr := f.AddRequire(p.Path, p.To); addErr != nil {
				return nil, fmt.Errorf("bumpOwnRequires: AddRequire %s: %w", p.Path, addErr)
			}
		}
		f.SortBlocks()
		f.Cleanup()
		out, err := f.Format()
		if err != nil {
			return nil, fmt.Errorf("bumpOwnRequires: format %s: %w", dir, err)
		}
		if err := os.WriteFile(modPath, out, 0o644); err != nil { //nolint:gosec // go.mod must be world-readable
			return nil, fmt.Errorf("bumpOwnRequires: write %s: %w", dir, err)
		}
		bumped = append(bumped, dir)
	}
	sort.Strings(bumped)
	return bumped, nil
}

// commitPaths stages the listed paths (relative to root) and
// records one commit with the supplied message. The caller is
// responsible for enumerating exactly what changed — `git add
// -A` is intentionally not used so the bump commit never picks
// up unrelated edits a developer might have in the worktree.
//
// `git add` runs in the standard buffered mode (no user
// interaction expected). `git commit` runs with the caller's
// terminal inherited because `commit.gpgsign=true` makes git
// invoke ssh-keygen / gpg, which needs a TTY for passphrase or
// hardware-key touch prompts.
//
// # Hooks
//
// The commit carries --no-verify. Development hooks assert a
// workspace-wide invariant — every module tidy against published
// versions — that holds only at the pipeline's entry and exit.
// Between the first layer's commit and the last, some dependent
// always pins a sibling older than the content its imports were
// built against, because `go mod tidy` resolves siblings through
// the proxy and ignores `go.work` entirely. A gate asserting that
// invariant at an interior commit fails by construction, and it
// deadlocked the eidos v1.3.1 release. The invariant is checked
// where it holds: the pipeline refuses to start on a dirty tree,
// and `ergon check` runs before the release.
//
// The one guarantee lost is the commit-msg hook's convention
// check; [PinCommitMessage] is validated against the repository's
// own `checks.commit_msg` policy at entry instead.
func commitPaths(ctx context.Context, runner xexec.Runner, root string, paths []string, msg string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	var buf bytes.Buffer
	if err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &buf, Stderr: &buf},
		"git", args...); err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(buf.String()))
	}
	return runGitInteractive(ctx, runner, root, "commit", "--no-verify", "-m", msg)
}

// planVersions maps each module directory to the version dependents
// should pin it at, and reports how many entries will be tagged.
//
// A skipped module contributes the version it already sits on, so a
// released dependent pins it at its real tag rather than dropping
// the requirement. The count is returned separately because the map
// is no longer a proxy for "is there anything to do" once skipped
// entries populate it.
func planVersions(plan []PlanEntry) (map[string]string, int) {
	versions := map[string]string{}
	toTag := 0
	for _, e := range plan {
		if !e.Skipped() {
			versions[e.Module.Dir] = e.NewVersion
			toTag++
			continue
		}
		// initialVersion means the module has never been tagged, so
		// there is no released version for a dependent to pin to.
		if e.OldVersion != "" && e.OldVersion != initialVersion {
			versions[e.Module.Dir] = e.OldVersion
		}
	}
	return versions, toTag
}

// releasedVersionMap builds the import-path → "vX.Y.Z" map for
// every resolved module. Keyed by import path because that is what
// a go.mod `require` directive reads, not by module directory.
func releasedVersionMap(
	root string, versions map[string]string, resolved map[string]bool,
) (map[string]string, error) {
	out := map[string]string{}
	for dir, ver := range versions {
		if !resolved[dir] {
			continue
		}
		ip, err := readModulePath(filepath.Join(root, dir, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("release: read module path for %s: %w", dir, err)
		}
		out[ip] = "v" + ver
	}
	return out, nil
}

// pinChanges reports the require rewrites moduleDir needs, reading
// its go.mod and writing nothing. Shared by the planner, which
// discloses them, and by [bumpOwnRequires], which applies them.
func pinChanges(root, moduleDir string, want map[string]string) ([]Pin, error) {
	modPath := filepath.Join(root, moduleDir, "go.mod")
	body, err := os.ReadFile(modPath)
	if err != nil {
		return nil, fmt.Errorf("release: read %s: %w", moduleDir, err)
	}
	f, err := modfile.Parse(modPath, body, nil)
	if err != nil {
		return nil, fmt.Errorf("release: parse %s: %w", moduleDir, err)
	}
	var out []Pin
	for _, r := range f.Require {
		if r == nil {
			continue
		}
		to, ok := want[r.Mod.Path]
		if !ok || r.Mod.Version == to {
			continue
		}
		out = append(out, Pin{Path: r.Mod.Path, From: r.Mod.Version, To: to})
	}
	return out, nil
}

// readModulePath reads the `module` directive from a single
// go.mod. Centralised so the bumper and dep-grapher agree on the
// import-path discovery rule.
func readModulePath(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return modfile.ModulePath(body), nil
}

// planEntry finds the PlanEntry for dir. Panics on a miss because
// the caller has already iterated plan to discover dir — a miss
// here would be a programming error, not a runtime fault.
func planEntry(plan []PlanEntry, dir string) PlanEntry {
	for _, e := range plan {
		if e.Module.Dir == dir {
			return e
		}
	}
	panic("planEntry: unknown dir " + dir)
}

// pinCommitSubject is the constant subject of every pin commit.
//
// Constant, and not "... for <tags>", because the tag list is
// unbounded: a ten-module layer produced a subject far past the
// 72-byte default `checks.commit_msg.max_subject_length`, and the
// repository's own commit-msg hook then rejected the pipeline's
// commit. The tags moved to the body, where each occupies its own
// short line.
const pinCommitSubject = "chore(release): pin intra-workspace deps"

// PinCommitMessage renders the commit message for one layer's
// propagation. The subject is constant; the body lists the tags
// whose versions the commit writes into their dependents, one per
// line.
//
// The tags named are the ones this layer just published, not the
// modules the commit edits: the commit exists because those
// versions became real, and naming them is what lets a reader match
// the commit to the push above it.
//
// Exported so the cobra layer can validate the generated message
// against the repository's `checks.commit_msg` policy before the
// release starts — the pipeline commits with --no-verify, so a
// policy that rejects `chore` has to fail at entry rather than
// land a non-conforming commit silently.
func PinCommitMessage(tags []string) string {
	var b strings.Builder
	b.WriteString(pinCommitSubject)
	b.WriteString("\n\nPins every intra-workspace require naming these modules to\n")
	b.WriteString("the versions this layer tagged and pushed.\n\nVersions propagated:\n")
	for _, t := range tags {
		b.WriteString("  - ")
		b.WriteString(t)
		b.WriteString("\n")
	}
	return b.String()
}

// layerFailure wraps a mid-pipeline git error with the identity of
// the layer in flight, the step that failed, and the resume path.
//
// A release that dies partway leaves tags on disk and on the
// remote; the bare `exit status 1` the pipeline used to return
// forced the operator to reconstruct which layer was in flight
// from tag timestamps. EnsureTag is idempotent, so re-running is
// the correct recovery and the message says so.
func layerFailure(ready []string, plan []PlanEntry, step string, err error) error {
	return fmt.Errorf(
		"release: git %s failed while releasing %s: %w\n"+
			"  tags already created are preserved; re-run `ergon release` to resume",
		step, strings.Join(tagNames(ready, plan), ", "), err)
}

// tagNames returns the tag-name list for the ready modules so the
// commit message can name what just got pinned.
func tagNames(ready []string, plan []PlanEntry) []string {
	out := make([]string, 0, len(ready))
	for _, dir := range ready {
		out = append(out, planEntry(plan, dir).Tag)
	}
	return out
}
