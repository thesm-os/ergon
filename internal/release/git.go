// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// commitTrailerSentinel separates `git log --format` records so the
// caller can split the captured output into per-commit chunks
// without colliding with anything a commit body might contain.
const commitTrailerSentinel = "----RELEASE-COMMIT----"

// LastTag returns the most recent semver-shaped tag whose name
// starts with prefix (the empty string for the root module,
// `foo/bar/` for a submodule). Returns the empty string when no
// matching tag exists.
//
// The selection is `--sort=-v:refname` so `v10.0.0` sorts after
// `v9.0.0` the way semver requires.
func LastTag(ctx context.Context, runner xexec.Runner, root, prefix string) (string, error) {
	pattern := prefix + "v[0-9]*"
	out, err := runGit(ctx, runner, root, "tag", "--list", pattern, "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", nil
	}
	return lines[0], nil
}

// ScopedCommits returns the commits between sinceTag and HEAD that
// touched any of includePaths, excluding any of excludePaths. Used
// to scope conventional-commit inference to a single module's
// history without picking up commits that landed inside a nested
// submodule's tree.
//
// sinceTag may be the empty string to mean "the full history of
// the included paths"; the function elides the `..HEAD` suffix in
// that case so git interprets the range as "every commit reachable
// from HEAD" instead of "every commit reachable from <empty>".
func ScopedCommits(
	ctx context.Context, runner xexec.Runner, root, sinceTag string,
	includePaths, excludePaths []string,
) ([]CommitInfo, error) {
	args := []string{
		"log",
		"--format=%s%n%b%n" + commitTrailerSentinel,
	}
	if sinceTag != "" {
		args = append(args, sinceTag+"..HEAD")
	}
	args = append(args, "--")
	args = append(args, includePaths...)
	for _, ex := range excludePaths {
		args = append(args, ":(exclude)"+ex)
	}
	out, err := runGit(ctx, runner, root, args...)
	if err != nil {
		return nil, err
	}
	return parseScopedCommits(out), nil
}

// parseScopedCommits splits the raw `git log` output produced by
// [ScopedCommits] into one [CommitInfo] per recorded commit. The
// trailing sentinel separates records cleanly; empty fragments
// (no commits, trailing blank) are dropped.
func parseScopedCommits(raw string) []CommitInfo {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, commitTrailerSentinel)
	commits := make([]CommitInfo, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimLeft(p, "\n")
		p = strings.TrimRight(p, "\n")
		if p == "" {
			continue
		}
		subject, body, _ := strings.Cut(p, "\n")
		commits = append(commits, CommitInfo{
			Subject: subject,
			Body:    strings.TrimSpace(body),
		})
	}
	return commits
}

// Tag creates an annotated tag named name with body message. The
// commit it points at is the current HEAD; the caller arranges for
// the bump commit to be HEAD before calling.
//
// Prefer [EnsureTag] over Tag at call sites that may be retried —
// EnsureTag is idempotent when the tag already exists at HEAD,
// which is exactly the state a partial prior run leaves behind.
func Tag(ctx context.Context, runner xexec.Runner, root, name, message string) error {
	_, err := runGit(ctx, runner, root, "tag", "-a", "-m", message, name)
	return err
}

// TagCommit returns the commit a tag points at and whether the
// tag exists. For an annotated tag the returned SHA is the
// underlying commit (the tag object itself is dereferenced via
// `git rev-list -n 1`); for a lightweight tag the SHA is the
// referenced commit directly. Used by [EnsureTag] to detect tags
// left behind by a prior partial run so retries can skip them.
func TagCommit(ctx context.Context, runner xexec.Runner, root, name string) (commit string, exists bool, err error) {
	out, err := runGit(ctx, runner, root, "tag", "-l", name)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(out) == "" {
		return "", false, nil
	}
	out, err = runGit(ctx, runner, root, "rev-list", "-n", "1", "refs/tags/"+name)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(out), true, nil
}

// HeadCommit returns the commit SHA at HEAD. Used by [EnsureTag]
// to verify an existing tag points at the commit we want to tag,
// rather than silently moving a published tag.
func HeadCommit(ctx context.Context, runner xexec.Runner, root string) (string, error) {
	out, err := runGit(ctx, runner, root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// EnsureTag creates an annotated tag named name with body message
// at the current HEAD, but is idempotent when the tag already
// exists at HEAD — exactly the state a prior partial release run
// leaves behind. Three outcomes:
//
//   - Tag does not exist: creates it via [Tag].
//   - Tag exists at HEAD: returns nil (no-op).
//   - Tag exists at a different commit: returns an error so the
//     caller surfaces the conflict rather than silently moving
//     a published tag.
//
// The error path on commit-mismatch carries shortened SHAs of
// both the existing tag's commit and HEAD so the user can locate
// the discrepancy without re-running git themselves.
func EnsureTag(ctx context.Context, runner xexec.Runner, root, name, message string) error {
	existing, exists, err := TagCommit(ctx, runner, root, name)
	if err != nil {
		return err
	}
	if exists {
		head, err := HeadCommit(ctx, runner, root)
		if err != nil {
			return err
		}
		if existing == head {
			return nil // idempotent: already at the right commit
		}
		return fmt.Errorf(
			"release: tag %s already exists at %s but HEAD is %s; "+
				"delete the tag or rewind HEAD before retrying",
			name, shortSHA(existing), shortSHA(head),
		)
	}
	return Tag(ctx, runner, root, name, message)
}

// PreflightTagSigning probes whether `git tag -a` can complete
// against the current repository configuration. Called at the
// start of the release pipeline so a broken signing setup fails
// fast — much cleaner than aborting mid-release with half-created
// tags requiring manual cleanup.
//
// The probe creates an ephemeral annotated tag named with a
// reserved sentinel, then deletes it. Both steps must succeed for
// the preflight to pass; either failure wraps the underlying git
// error with a hint about the most common cause (GPG agent
// unavailable, signing key missing).
//
// Safe to call repeatedly: a leftover probe tag from a previous
// aborted preflight is cleaned up before the new probe runs.
func PreflightTagSigning(ctx context.Context, runner xexec.Runner, root string) error {
	const probeName = "__ergon_release_signing_probe__"

	// Defensively clean up a leftover probe from a prior aborted
	// run before creating a fresh one. Ignore errors: the tag
	// likely doesn't exist (which is the desired starting state).
	_, _ = runGit(ctx, runner, root, "tag", "-d", probeName)

	if _, err := runGit(ctx, runner, root, "tag", "-a", "-m", "ergon signing preflight", probeName); err != nil {
		return fmt.Errorf(
			"release: tag-signing preflight failed (check GPG / signing-key setup): %w",
			err,
		)
	}
	if _, err := runGit(ctx, runner, root, "tag", "-d", probeName); err != nil {
		return fmt.Errorf("release: clean up signing probe tag %q: %w", probeName, err)
	}
	return nil
}

// shortSHA truncates a git SHA to the first 12 characters for
// human-readable error messages. Returns the input unchanged when
// it is shorter than 12 characters (e.g. a synthetic SHA used in
// tests).
func shortSHA(sha string) string {
	if len(sha) >= 12 {
		return sha[:12]
	}
	return sha
}

// runGit shells out to git via runner with the supplied args. The
// returned string is the combined stdout/stderr the command
// produced; on failure the error wraps the captured output so
// callers see what git actually complained about.
func runGit(ctx context.Context, runner xexec.Runner, root string, args ...string) (string, error) {
	var out bytes.Buffer
	err := runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: &out, Stderr: &out},
		"git", args...)
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}
