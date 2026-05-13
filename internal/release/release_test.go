// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import (
	"errors"
	"io"
	"strings"
	"testing"

	"go.thesmos.sh/ergon/internal/modules"
)

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
		runner := &planFakeRunner{}
		var buf strings.Builder
		err := Run(t.Context(), runner, &buf, "/repo",
			[]modules.Module{{Dir: "cli"}},
			Options{Force: BumpPatch, Message: "rel"})
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		// Two calls: LastTag for the module, then `git tag -a`.
		if len(runner.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(runner.calls))
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
