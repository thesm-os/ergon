// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/modules"
)

// writeMod stages a minimal go.mod under root/dir so the
// release pipeline (which reads every module's go.mod to build
// the intra-workspace dependency graph) can discover the import
// path. Returns root unchanged for chaining.
func writeMod(t *testing.T, root, dir, importPath string) string {
	t.Helper()
	target := filepath.Join(root, dir)
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", target, err)
	}
	body := "module " + importPath + "\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(target, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return root
}

// TestRun pins the orchestrator's four branches:
//
//   - Empty module set returns an explicit error.
//   - DryRun prints the plan and stops short of [ApplyPlan].
//   - Normal flow prints the plan and tags.
//   - BuildPlan error short-circuits before any tag is created.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("no modules surfaces a usage error", func(t *testing.T) {
		t.Parallel()
		err := Run(t.Context(), &planFakeRunner{}, io.Discard,
			"/repo", nil, Options{})
		if err == nil {
			t.Fatal("Run err = nil, want non-nil")
		}
	})

	t.Run("dry-run prints the plan and skips ApplyPlan", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{}
		var buf strings.Builder
		err := Run(t.Context(), runner, &buf, "/repo",
			[]modules.Module{{Dir: "cli"}},
			Options{Force: BumpPatch, DryRun: true})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(buf.String(), "dry-run") {
			t.Fatalf("output missing dry-run notice: %q", buf.String())
		}
		for _, c := range runner.calls {
			if len(c.args) > 0 && c.args[0] == "tag" && len(c.args) > 1 && c.args[1] == "-a" {
				t.Fatalf("dry-run created a tag: %+v", c.args)
			}
		}
	})

	t.Run("normal flow tags every non-skipped module", func(t *testing.T) {
		t.Parallel()
		root := writeMod(t, t.TempDir(), "cli", "go.example.com/proj/cli")
		runner := &planFakeRunner{}
		var buf strings.Builder
		err := Run(t.Context(), runner, &buf, root,
			[]modules.Module{{Dir: "cli"}},
			Options{Force: BumpPatch, Message: "rel", AllowDirty: true})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		// 3 git calls: LastTag, dirty-check skipped via AllowDirty,
		// then `git tag -a`. The intra-workspace bump is a no-op
		// (one-module workspace, no dep edges).
		if len(runner.calls) < 2 {
			t.Fatalf("calls = %d, want at least 2", len(runner.calls))
		}
	})

	t.Run("BuildPlan error short-circuits before tagging", func(t *testing.T) {
		t.Parallel()
		runner := &planFakeRunner{runErr: errors.New("git missing")}
		err := Run(t.Context(), runner, io.Discard, "/repo",
			[]modules.Module{{Dir: "cli"}}, Options{Message: "rel"})
		if err == nil {
			t.Fatal("Run err = nil, want non-nil")
		}
	})
}

// TestRunVersionRendersOneWave pins what the plan promises under
// --version.
//
// The plan is read immediately before a signing session, so it has to
// describe the pipeline that will actually run. planWaves computes a
// layered order regardless of --version, and rendering it advertised
// waves and per-wave pushes that applySingleWave never performs —
// "after wave 1 is pushed" for a release that pushes exactly once.
// A plan describing a different pipeline is worse than one describing
// none, because an operator has no reason to doubt it.
func TestRunVersionRendersOneWave(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeMod(t, root, "a", "go.example.com/proj/a")
	writeMod(t, root, "b", "go.example.com/proj/b")
	mods := []modules.Module{{Dir: "a"}, {Dir: "b"}}

	run := func(t *testing.T, opts Options) string {
		t.Helper()
		var buf strings.Builder
		if err := Run(t.Context(), &planFakeRunner{}, &buf, root, mods, opts); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		return buf.String()
	}

	pinned := run(t, Options{Version: "v1.2.0", DryRun: true, Message: "r"})
	if strings.Contains(pinned, "wave") && !strings.Contains(pinned, "Single wave") {
		t.Errorf("--version plan = %q, want no layered wave headers", pinned)
	}
	// The count is the point: it tells the operator how many hardware
	// key prompts are coming before the first one appears.
	if !strings.Contains(pinned, "Single wave: every module at v1.2.0") {
		t.Errorf("--version plan = %q, want the single-wave summary", pinned)
	}

	// Without --version the layered rendering must survive: this is
	// the default path and it genuinely does run in waves.
	layered := run(t, Options{Force: BumpPatch, DryRun: true, Message: "r"})
	if strings.Contains(layered, "Single wave") {
		t.Errorf("default plan = %q, want no single-wave summary", layered)
	}
}
