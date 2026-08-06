// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/modules"
)

// TestWorkspaceDeps pins the dependency-graph builder: each
// module's set is exactly the OTHER workspace modules its go.mod
// `require`s; self-loops are dropped; non-workspace requires
// (`golang.org/x/...`) are ignored.
func TestWorkspaceDeps(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceMod(t, root, ".", `module go.example.com/proj

go 1.26
`)
	writeWorkspaceMod(t, root, "cli", `module go.example.com/proj/cli

go 1.26

require (
	go.example.com/proj v1.2.0
	golang.org/x/mod v0.36.0
)
`)
	writeWorkspaceMod(t, root, "backend", `module go.example.com/proj/backend

go 1.26

require (
	go.example.com/proj v1.2.0
	go.example.com/proj/cli v1.0.0
)
`)

	mods := []modules.Module{{Dir: "."}, {Dir: "cli"}, {Dir: "backend"}}
	got, err := workspaceDeps(root, mods)
	if err != nil {
		t.Fatalf("workspaceDeps err: %v", err)
	}
	if len(got["."]) != 0 {
		t.Errorf("root deps = %+v, want empty (leaf)", got["."])
	}
	if !got["cli"]["."] || len(got["cli"]) != 1 {
		t.Errorf("cli deps = %+v, want only root", got["cli"])
	}
	if !got["backend"]["."] || !got["backend"]["cli"] || len(got["backend"]) != 2 {
		t.Errorf("backend deps = %+v, want root + cli", got["backend"])
	}
}

// TestLayerReady pins the topological-leaf selector: modules
// whose dependencies are all already released are picked up;
// blocked ones wait their turn. Skipped entries count as
// resolved so the layer doesn't stall on them.
func TestLayerReady(t *testing.T) {
	t.Parallel()

	plan := []PlanEntry{
		{Module: modules.Module{Dir: "."}, NewVersion: "1.3.0", Tag: "v1.3.0"},
		{Module: modules.Module{Dir: "cli"}, NewVersion: "1.0.0", Tag: "cli/v1.0.0"},
		{Module: modules.Module{Dir: "backend"}, NewVersion: "2.0.0", Tag: "backend/v2.0.0"},
	}
	deps := map[string]map[string]bool{
		".":       {},
		"cli":     {".": true},
		"backend": {".": true, "cli": true},
	}

	// Iteration 1: only root is ready.
	ready := layerReady(plan, deps, map[string]bool{})
	if len(ready) != 1 || ready[0] != "." {
		t.Fatalf("layer 1 = %+v, want [.]", ready)
	}

	// Iteration 2: with root released, cli unblocks; backend
	// still waits on cli.
	released := map[string]bool{".": true}
	ready = layerReady(plan, deps, released)
	if len(ready) != 1 || ready[0] != "cli" {
		t.Fatalf("layer 2 = %+v, want [cli]", ready)
	}

	// Iteration 3: with cli released too, backend unblocks.
	released["cli"] = true
	ready = layerReady(plan, deps, released)
	if len(ready) != 1 || ready[0] != "backend" {
		t.Fatalf("layer 3 = %+v, want [backend]", ready)
	}
}

// TestBumpOwnRequires pins the modfile rewrite: a module about
// to be tagged updates its own go.mod require line for every
// already-released workspace dependency. Other requires are
// untouched.
func TestBumpOwnRequires(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceMod(t, root, ".", `module go.example.com/proj

go 1.26
`)
	writeWorkspaceMod(t, root, "cli", `module go.example.com/proj/cli

go 1.26

require (
	go.example.com/proj v1.2.0
	golang.org/x/mod v0.36.0
)
`)

	versions := map[string]string{".": "1.3.0", "cli": "1.0.0"}
	released := map[string]bool{".": true} // root is released, cli is about to be

	bumped, err := bumpOwnRequires(root, []string{"cli"}, versions, released)
	if err != nil {
		t.Fatalf("bumpOwnRequires err: %v", err)
	}
	if len(bumped) != 1 || bumped[0] != "cli" {
		t.Fatalf("bumped = %+v, want [cli]", bumped)
	}

	body, err := os.ReadFile(filepath.Join(root, "cli", "go.mod"))
	if err != nil {
		t.Fatalf("read cli/go.mod: %v", err)
	}
	if !strings.Contains(string(body), "v1.3.0") {
		t.Fatalf("cli/go.mod missing v1.3.0:\n%s", body)
	}
	if strings.Contains(string(body), "v1.2.0") {
		t.Fatalf("cli/go.mod still references v1.2.0:\n%s", body)
	}
	// Unrelated `require` should survive untouched.
	if !strings.Contains(string(body), "golang.org/x/mod v0.36.0") {
		t.Fatalf("cli/go.mod lost the unrelated require:\n%s", body)
	}
}

// writeWorkspaceMod stages a go.mod file under root/dir with the
// supplied body. Creates the directory as needed.
func writeWorkspaceMod(t *testing.T, root, dir, body string) {
	t.Helper()
	target := filepath.Join(root, dir)
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", target, err)
	}
	if err := os.WriteFile(filepath.Join(target, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

// TestHEADIsDirty pins the working-tree state classifier: a
// `git status --porcelain` that prints anything (after trim) is
// dirty; empty output is clean. The wrapped error path is
// covered too.
func TestHEADIsDirty(t *testing.T) {
	t.Parallel()

	t.Run("empty status output reports clean", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: ""}
		dirty, err := HEADIsDirty(t.Context(), runner, "/repo")
		if err != nil {
			t.Fatalf("HEADIsDirty err: %v", err)
		}
		if dirty {
			t.Error("dirty = true, want false")
		}
	})

	t.Run("non-empty status output reports dirty", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: " M go.mod\n"}
		dirty, err := HEADIsDirty(t.Context(), runner, "/repo")
		if err != nil {
			t.Fatalf("HEADIsDirty err: %v", err)
		}
		if !dirty {
			t.Error("dirty = false, want true")
		}
	})

	t.Run("whitespace-only output is clean (trim semantics)", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: "  \n  \n"}
		dirty, err := HEADIsDirty(t.Context(), runner, "/repo")
		if err != nil {
			t.Fatalf("HEADIsDirty err: %v", err)
		}
		if dirty {
			t.Error("dirty = true, want false for whitespace-only output")
		}
	})

	t.Run("git failure surfaces wrapped", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: "fatal: not a repo", runErr: errors.New("exit 128")}
		_, err := HEADIsDirty(t.Context(), runner, "/repo")
		if err == nil {
			t.Fatal("HEADIsDirty err = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "fatal: not a repo") {
			t.Errorf("err = %v, want it to wrap the captured stderr", err)
		}
	})
}

// TestCommitPaths pins the `git add -- <paths>` + `git commit
// -m <msg>` two-step shape. Empty paths short-circuits without
// invoking git.
func TestCommitPaths(t *testing.T) {
	t.Parallel()

	t.Run("empty paths is a no-op (no git invocation)", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		if err := commitPaths(t.Context(), runner, "/repo", nil, "msg"); err != nil {
			t.Fatalf("commitPaths err: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Errorf("calls = %d, want 0 (no-op on empty paths)", len(runner.calls))
		}
	})

	t.Run("non-empty paths runs add then commit", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		err := commitPaths(t.Context(), runner, "/repo",
			[]string{"cli/go.mod", "cli/go.sum"}, "chore(release): test")
		if err != nil {
			t.Fatalf("commitPaths err: %v", err)
		}
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2 (add + commit)", len(runner.calls))
		}
		addArgs := runner.calls[0].args
		wantAdd := []string{"add", "--", "cli/go.mod", "cli/go.sum"}
		if !slices.Equal(addArgs, wantAdd) {
			t.Errorf("add args = %+v, want %+v", addArgs, wantAdd)
		}
		// --no-verify: the pipeline's own commits bypass development
		// hooks, whose workspace-wide tidy invariant cannot hold at an
		// interior layer. See the Hooks section on commitPaths.
		commitArgs := runner.calls[1].args
		wantCommit := []string{"commit", "--no-verify", "-m", "chore(release): test"}
		if !slices.Equal(commitArgs, wantCommit) {
			t.Errorf("commit args = %+v, want %+v", commitArgs, wantCommit)
		}
	})

	t.Run("git add failure wraps stderr", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: "fatal: pathspec did not match", runErr: errors.New("exit 128")}
		err := commitPaths(t.Context(), runner, "/repo", []string{"missing.go"}, "msg")
		if err == nil {
			t.Fatal("commitPaths err = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "fatal: pathspec") {
			t.Errorf("err = %v, want it to wrap the captured stderr", err)
		}
	})
}

// TestTidyModules pins the per-module `go mod tidy` shape:
// one invocation per directory, working dir set to each module
// root, the first error short-circuits the rest.
func TestTidyModules(t *testing.T) {
	t.Parallel()

	t.Run("runs go mod tidy in each module dir in order", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		if err := tidyModules(
			t.Context(), runner, "/repo",
			[]string{"cli", "frontend/golang"}, nil,
		); err != nil {
			t.Fatalf("tidyModules err: %v", err)
		}
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(runner.calls))
		}
		for _, c := range runner.calls {
			wantArgs := []string{"mod", "tidy"}
			if !slices.Equal(c.args, wantArgs) {
				t.Errorf("args = %+v, want %+v", c.args, wantArgs)
			}
		}
	})

	t.Run("own modules resolve from git, not the public proxy", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		own := []string{"example.test/proj", "example.test/proj/cli"}
		if err := tidyModules(t.Context(), runner, "/repo", []string{"cli"}, own); err != nil {
			t.Fatalf("tidyModules err: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(runner.calls))
		}
		// Both variables, because the proxy supplies the version
		// listing and the sumdb verifies the download: leaving
		// either pointed at the public service reintroduces the
		// dependency on a cache observing a seconds-old tag.
		got := strings.Join(runner.calls[0].env, " ")
		for _, want := range []string{
			"GONOPROXY=example.test/proj,example.test/proj/cli",
			"GONOSUMDB=example.test/proj,example.test/proj/cli",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("env = %q, want it to contain %q", got, want)
			}
		}
	})

	t.Run("no workspace paths leaves the environment untouched", func(t *testing.T) {
		t.Parallel()
		if env := directFetchEnv(nil); env != nil {
			t.Errorf("directFetchEnv(nil) = %v, want nil", env)
		}
	})

	t.Run("first failure surfaces wrapped with the module dir", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{output: "go: cannot find module", runErr: errors.New("exit 1")}
		err := tidyModules(t.Context(), runner, "/repo", []string{"cli"}, nil)
		if err == nil {
			t.Fatal("tidyModules err = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "cli") {
			t.Errorf("err = %v, want it to mention the failing module dir", err)
		}
		if !strings.Contains(err.Error(), "go: cannot find module") {
			t.Errorf("err = %v, want it to wrap the captured stderr", err)
		}
	})
}

// TestWorkspaceModulePathsMissingGoMod pins that a module absent
// from disk is reported rather than silently omitted.
//
// The result scopes which paths bypass the proxy. A module dropped
// from that list would be resolved through proxy.golang.org, which
// is exactly the dependency on a third-party cache the list exists
// to remove — and it would fail only sometimes.
func TestWorkspaceModulePathsMissingGoMod(t *testing.T) {
	t.Parallel()

	_, err := workspaceModulePaths(t.TempDir(), []modules.Module{{Dir: "gone"}})
	if err == nil {
		t.Error("workspaceModulePaths = nil, want the missing go.mod reported")
	}
}

// TestDirectFetchEnvPreservesOperatorConfig pins that ergon extends
// an operator's GONOPROXY rather than replacing it.
//
// Top-level and not parallel because t.Setenv forbids parallelism
// and the surrounding TestTidyModules is parallel. It earns its own
// test rather than being dropped: an operator with GONOPROXY set for
// their own private modules would, on a clobber, have that code
// silently routed through the public proxy by a release — a
// disclosure no other assertion here would catch.
func TestDirectFetchEnvPreservesOperatorConfig(t *testing.T) {
	t.Setenv("GONOPROXY", "corp.example/internal")
	t.Setenv("GONOSUMDB", "corp.example/internal")

	got := strings.Join(directFetchEnv([]string{"example.test/proj"}), " ")
	for _, want := range []string{
		"GONOPROXY=corp.example/internal,example.test/proj",
		"GONOSUMDB=corp.example/internal,example.test/proj",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("env = %q, want it to contain %q", got, want)
		}
	}
}

// TestBumpedPaths pins the go.mod + (conditional) go.sum
// enumeration for each bumped module: go.mod is always emitted,
// go.sum is emitted only when the file exists on disk (tidy on
// a module with no external requires may not write one).
func TestBumpedPaths(t *testing.T) {
	t.Parallel()

	t.Run("emits go.mod and go.sum when both exist", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeWorkspaceMod(t, root, "cli", "module example.com/proj/cli\n\ngo 1.26\n")
		if err := os.WriteFile(filepath.Join(root, "cli", "go.sum"), nil, 0o600); err != nil {
			t.Fatalf("seed go.sum: %v", err)
		}
		got := bumpedPaths(root, []string{"cli"})
		// bumpedPaths produces filepath-joined results, so the want
		// list is built the same way to stay portable across `/`
		// (Linux/macOS) and `\` (Windows) separators.
		want := []string{filepath.Join("cli", "go.mod"), filepath.Join("cli", "go.sum")}
		if !slices.Equal(got, want) {
			t.Errorf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("emits go.mod alone when go.sum is missing", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeWorkspaceMod(t, root, "cli", "module example.com/proj/cli\n\ngo 1.26\n")
		// No go.sum written.
		got := bumpedPaths(root, []string{"cli"})
		want := []string{filepath.Join("cli", "go.mod")}
		if !slices.Equal(got, want) {
			t.Errorf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("preserves input order across multiple modules", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		for _, d := range []string{"a", "b", "c"} {
			writeWorkspaceMod(t, root, d, "module example.com/proj/"+d+"\n\ngo 1.26\n")
		}
		got := bumpedPaths(root, []string{"a", "b", "c"})
		want := []string{
			filepath.Join("a", "go.mod"),
			filepath.Join("b", "go.mod"),
			filepath.Join("c", "go.mod"),
		}
		if !slices.Equal(got, want) {
			t.Errorf("got = %+v, want %+v", got, want)
		}
	})
}

// TestTagNames pins the plan-entry → tag-name extraction the
// commit-message composer relies on.
func TestTagNames(t *testing.T) {
	t.Parallel()

	plan := []PlanEntry{
		{Module: modules.Module{Dir: "."}, Tag: "v1.0.0"},
		{Module: modules.Module{Dir: "cli"}, Tag: "cli/v0.2.0"},
		{Module: modules.Module{Dir: "skipped"}, Tag: ""}, // skipped — not in ready
	}
	got := tagNames([]string{".", "cli"}, plan)
	want := []string{"v1.0.0", "cli/v0.2.0"}
	if !slices.Equal(got, want) {
		t.Errorf("got = %+v, want %+v", got, want)
	}
}
