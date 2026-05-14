// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

//go:build e2e

// This file holds the end-to-end test for [ApplyPipeline] — the
// only test in the release package that exercises real `git`
// against a real working tree. It gates behind the `e2e` build
// tag so `go test ./...` (and ergon's own `check` umbrella)
// remain fast; CI invokes `go test -tags=e2e ./internal/release/...`
// on a separate schedule, or whenever the release package
// changes.
//
// The unit tests in this package cover invocation shape via
// [gitFakeRunner]; this file's job is to verify the pieces
// actually compose against a real git binary. Specifically, it
// pins the bump-rewrite-commit-tag path that has the highest
// silent-corruption risk: a unit test that asserts "the right
// go install args were passed" cannot catch a regression where
// the modfile rewriter produces malformed go.mod content.

package release_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/modules"
	"go.thesmos.sh/ergon/internal/release"
)

// TestApplyPipelineEndToEnd exercises the full bump-rewrite-
// commit-tag flow against a real git binary on a synthetic
// two-module workspace:
//
//   - root module example.com/myrepo at ./go.mod
//   - submodule example.com/myrepo/cli at ./cli/go.mod, which
//     requires root via a `require example.com/myrepo v0.0.0`
//     line.
//
// The initial commit's subject is `feat: initial` so the
// conventional-commit inference picks BumpMinor for both modules.
// NoPush=true is set on Options because the test runs in an
// offline tempdir without a remote — that knob skips the
// `git push` and the `go mod tidy` that follows each layer.
// Every other step of the pipeline (modfile rewrite, the bump
// commit, the annotated tags) still runs against real git.
//
// Assertions cover the contract the unit tests cannot pin:
//
//   - The root and cli modules each receive an annotated tag
//     in the canonical Go multi-module shape (`v0.1.0`,
//     `cli/v0.1.0`).
//   - cli/go.mod's `require example.com/myrepo` line is
//     rewritten to v0.1.0 — the dep-bump path that touches
//     real source on disk.
//   - The bump commit lands with a `chore(release):` subject
//     and is referenced by the submodule's tag (root is tagged
//     at the original HEAD because it has no intra-workspace
//     dependencies of its own).
func TestApplyPipelineEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping e2e release test")
	}

	root := t.TempDir()
	runner := xexec.Command{}
	ctx := t.Context()

	// Build a synthetic workspace. Both go.mod files must be
	// valid for golang.org/x/mod/modfile to parse them; the
	// module paths do not have to be resolvable on the proxy
	// because NoPush=true skips `go mod tidy`.
	writeFile(t, root, "go.mod", `module example.com/myrepo

go 1.26
`)
	writeFile(t, root, "main.go", "package myrepo\n")
	writeFile(t, root, "cli/go.mod", `module example.com/myrepo/cli

go 1.26

require example.com/myrepo v0.0.0
`)
	writeFile(t, root, "cli/main.go", "package main\n")

	// Initial git state — a single feat: commit so conventional-
	// commit inference picks BumpMinor for both modules.
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "user.name", "ergon-test")
	git(t, root, "config", "user.email", "test@example.com")
	git(t, root, "config", "commit.gpgsign", "false")
	git(t, root, "config", "tag.gpgsign", "false")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "feat: initial implementation")
	initialHEAD := strings.TrimSpace(gitOut(t, root, "rev-parse", "HEAD"))

	mods := []modules.Module{{Dir: "."}, {Dir: "cli"}}
	opts := release.Options{
		Message: "test release",
		NoPush:  true, // offline: skip `git push` and `go mod tidy`
	}

	plan, err := release.BuildPlan(ctx, runner, root, mods, opts)
	if err != nil {
		t.Fatalf("BuildPlan err: %v", err)
	}

	// Plan sanity: every entry should be tagged at v0.1.0 because
	// the only commit in scope is a `feat:` against initial 0.0.0.
	for _, e := range plan {
		if e.Skipped() {
			t.Fatalf("plan entry %+v is skipped, want a v0.1.0 entry", e)
		}
		if !strings.HasSuffix(e.NewVersion, "0.1.0") {
			t.Errorf("plan entry NewVersion = %q, want a 0.1.0 bump", e.NewVersion)
		}
	}

	if err := release.ApplyPipeline(ctx, runner, root, io.Discard, mods, plan, opts); err != nil {
		t.Fatalf("ApplyPipeline err: %v", err)
	}

	// Root module: annotated tag named `v0.1.0`.
	assertTagExists(t, root, "v0.1.0")
	if got := strings.TrimSpace(gitOut(t, root, "rev-parse", "v0.1.0^{commit}")); got != initialHEAD {
		// Root has no intra-workspace dependencies so its tag
		// must point at the initial commit, NOT at any
		// downstream bump commit.
		t.Errorf("v0.1.0 points at %s, want %s (initial HEAD)", got, initialHEAD)
	}

	// Submodule: annotated tag named `cli/v0.1.0` per the Go
	// multi-module tag convention.
	assertTagExists(t, root, "cli/v0.1.0")

	// The bump commit must exist between initialHEAD and the
	// submodule tag — the submodule's tag should point at a
	// `chore(release):` commit whose parent is initialHEAD.
	cliTagCommit := strings.TrimSpace(gitOut(t, root, "rev-parse", "cli/v0.1.0^{commit}"))
	if cliTagCommit == initialHEAD {
		t.Errorf("cli/v0.1.0 points at initial commit; expected a bump commit between initial and the tag")
	}
	subject := strings.TrimSpace(gitOut(t, root, "log", "-1", "--format=%s", cliTagCommit))
	if !strings.HasPrefix(subject, "chore(release)") {
		t.Errorf("bump commit subject = %q, want a chore(release): prefix", subject)
	}

	// cli/go.mod's require line must have been rewritten from
	// v0.0.0 to v0.1.0 — the dep-bump path that touches real
	// source on disk.
	cliGoMod, err := os.ReadFile(filepath.Join(root, "cli", "go.mod"))
	if err != nil {
		t.Fatalf("read cli/go.mod: %v", err)
	}
	if !strings.Contains(string(cliGoMod), "example.com/myrepo v0.1.0") {
		t.Errorf("cli/go.mod missing rewritten require:\n%s", cliGoMod)
	}
	if strings.Contains(string(cliGoMod), "example.com/myrepo v0.0.0") {
		t.Errorf("cli/go.mod still contains the pre-bump v0.0.0 require:\n%s", cliGoMod)
	}

	// Both tags must be annotated, not lightweight. An annotated
	// tag has its own object with a tagger line; lightweight
	// tags do not.
	for _, name := range []string{"v0.1.0", "cli/v0.1.0"} {
		objType := strings.TrimSpace(gitOut(t, root, "cat-file", "-t", name))
		if objType != "tag" {
			t.Errorf("tag %q has object type %q, want %q (annotated)", name, objType, "tag")
		}
	}
}

// TestApplyPipelineIdempotentRetry pins the partial-failure
// recovery path. Simulates a prior run that created the root
// tag and then failed before tagging the submodule (a common
// shape for GPG / network / unrelated git failures mid-
// pipeline). The retry uses --version to pin the target value
// so [BuildPlan] does not bump past the partially-published
// version.
//
// Verifies:
//   - the already-existing root tag is preserved (not re-created
//     or moved);
//   - the submodule is tagged at the bump commit produced this
//     run;
//   - exactly one chore(release) commit lands (not one per run);
//   - the submodule's go.mod require is rewritten to the pinned
//     root version.
func TestApplyPipelineIdempotentRetry(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping e2e release test")
	}

	root := t.TempDir()
	runner := xexec.Command{}
	ctx := t.Context()

	writeFile(t, root, "go.mod", `module example.com/myrepo

go 1.26
`)
	writeFile(t, root, "main.go", "package myrepo\n")
	writeFile(t, root, "cli/go.mod", `module example.com/myrepo/cli

go 1.26

require example.com/myrepo v0.0.0
`)
	writeFile(t, root, "cli/main.go", "package main\n")

	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "user.name", "ergon-test")
	git(t, root, "config", "user.email", "test@example.com")
	git(t, root, "config", "commit.gpgsign", "false")
	git(t, root, "config", "tag.gpgsign", "false")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "feat: initial")
	initialHEAD := strings.TrimSpace(gitOut(t, root, "rev-parse", "HEAD"))

	// Simulate the partial-failure state: a prior run successfully
	// tagged root v0.1.0 at the initial commit, then aborted before
	// tagging the submodule.
	git(t, root, "tag", "-a", "v0.1.0", "-m", "prior partial run")

	// Retry with --version pinning the same target value. Without
	// the version pin, BuildPlan would compute v0.1.0 -> v0.1.1
	// for root (since root now has a tag) and split the release
	// across adjacent versions.
	mods := []modules.Module{{Dir: "."}, {Dir: "cli"}}
	opts := release.Options{
		Message: "retry the partial release",
		Version: "v0.1.0",
		NoPush:  true,
	}
	plan, err := release.BuildPlan(ctx, runner, root, mods, opts)
	if err != nil {
		t.Fatalf("BuildPlan err: %v", err)
	}
	if err := release.ApplyPipeline(ctx, runner, root, io.Discard, mods, plan, opts); err != nil {
		t.Fatalf("ApplyPipeline (retry) err: %v", err)
	}

	// Root tag still at the initial commit (idempotent skip).
	if got := strings.TrimSpace(gitOut(t, root, "rev-parse", "v0.1.0^{commit}")); got != initialHEAD {
		t.Errorf("v0.1.0 moved to %s; want it preserved at initial HEAD %s", got, initialHEAD)
	}

	// Submodule tag landed at a new commit (the bump commit).
	assertTagExists(t, root, "cli/v0.1.0")
	cliTagCommit := strings.TrimSpace(gitOut(t, root, "rev-parse", "cli/v0.1.0^{commit}"))
	if cliTagCommit == initialHEAD {
		t.Errorf("cli/v0.1.0 points at initial commit; expected bump commit between initial and tag")
	}

	// Exactly one chore(release) commit reachable from HEAD —
	// the retry must not produce a second one.
	choreCount := strings.TrimSpace(gitOut(t, root, "log", "--oneline", "--grep=chore(release)"))
	choreLines := strings.Split(choreCount, "\n")
	if len(choreLines) != 1 || choreLines[0] == "" {
		t.Errorf("found %d chore(release) commits, want exactly 1:\n%s", len(choreLines), choreCount)
	}

	// Submodule's go.mod require rewritten to the pinned version.
	cliGoMod, err := os.ReadFile(filepath.Join(root, "cli", "go.mod"))
	if err != nil {
		t.Fatalf("read cli/go.mod: %v", err)
	}
	if !strings.Contains(string(cliGoMod), "example.com/myrepo v0.1.0") {
		t.Errorf("cli/go.mod missing rewritten require to v0.1.0:\n%s", cliGoMod)
	}
}

// TestApplyPipelineRejectsDirtyTree pins the dirty-HEAD safety
// check. The bump pipeline creates its own commit; running with
// unrelated dirty files conflates them, which corrupts release
// history. With opts.AllowDirty=false (the default) ApplyPipeline
// must surface an error before any tag is created.
func TestApplyPipelineRejectsDirtyTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping e2e release test")
	}

	root := t.TempDir()
	runner := xexec.Command{}
	ctx := t.Context()

	writeFile(t, root, "go.mod", "module example.com/myrepo\n\ngo 1.26\n")
	writeFile(t, root, "main.go", "package myrepo\n")
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "user.name", "ergon-test")
	git(t, root, "config", "user.email", "test@example.com")
	git(t, root, "config", "commit.gpgsign", "false")
	git(t, root, "config", "tag.gpgsign", "false")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "feat: initial")

	// Make the tree dirty without committing.
	writeFile(t, root, "stray.txt", "uncommitted edit\n")

	mods := []modules.Module{{Dir: "."}}
	opts := release.Options{Message: "test", NoPush: true}
	plan, err := release.BuildPlan(ctx, runner, root, mods, opts)
	if err != nil {
		t.Fatalf("BuildPlan err: %v", err)
	}

	err = release.ApplyPipeline(ctx, runner, root, io.Discard, mods, plan, opts)
	if err == nil {
		t.Fatal("ApplyPipeline returned nil, want a dirty-tree error")
	}
	// Sanity: no tag should have been created.
	out := strings.TrimSpace(gitOut(t, root, "tag", "--list"))
	if out != "" {
		t.Errorf("git tag --list = %q, want empty (no tags should land on dirty tree)", out)
	}
	// And the error must not look like a "this is fine" — we want
	// it to mention the dirty state explicitly enough for the user
	// to act on it.
	if !strings.Contains(strings.ToLower(err.Error()), "dirty") &&
		!strings.Contains(strings.ToLower(err.Error()), "uncommitted") {
		t.Errorf("err = %v, want a dirty-tree-specific message", err)
	}
}

// writeFile writes content under dir/path, creating any missing
// parent directories. Failures fail the test.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// git runs `git <args...>` in dir and fails the test on a non-
// zero exit code. Stdout and stderr are reported in the failure
// message so debugging across the build farm stays direct.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
}

// gitOut runs `git <args...>` in dir and returns its stdout. Test
// fails on a non-zero exit code; the caller is responsible for
// trimming whitespace where required (most git output ends in a
// newline).
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

// assertTagExists fails the test when name is not present in
// `git tag --list`.
func assertTagExists(t *testing.T, dir, name string) {
	t.Helper()
	out := gitOut(t, dir, "tag", "--list", name)
	if strings.TrimSpace(out) == "" {
		t.Fatalf("tag %q does not exist; `git tag --list` returned %q",
			name, strings.TrimSpace(gitOut(t, dir, "tag", "--list")))
	}
}
