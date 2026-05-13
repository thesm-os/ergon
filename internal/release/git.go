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
func Tag(ctx context.Context, runner xexec.Runner, root, name, message string) error {
	_, err := runGit(ctx, runner, root, "tag", "-a", "-m", message, name)
	return err
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
