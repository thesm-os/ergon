// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"os"
	"path/filepath"
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
