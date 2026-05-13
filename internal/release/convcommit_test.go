// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

import "testing"

// TestClassifyCommits pins the conventional-commit → BumpLevel
// mapping. Each subtest exercises one rule; the final subtest
// exercises the "maximum across the set" behaviour.
func TestClassifyCommits(t *testing.T) {
	t.Parallel()

	t.Run("breaking subject with !: marker drives major", func(t *testing.T) {
		t.Parallel()
		for _, subject := range []string{
			"feat!: drop deprecated API",
			"fix!: remove the v1 shape",
			"refactor(api)!: rename Foo to Bar",
		} {
			got := ClassifyCommits([]CommitInfo{{Subject: subject}})
			if got != BumpMajor {
				t.Fatalf("Classify(%q) = %v, want BumpMajor", subject, got)
			}
		}
	})

	t.Run("BREAKING CHANGE in body drives major even without !:", func(t *testing.T) {
		t.Parallel()
		got := ClassifyCommits([]CommitInfo{{
			Subject: "fix: thing",
			Body:    "Repairs the routing layer.\n\nBREAKING CHANGE: the old config field is removed.",
		}})
		if got != BumpMajor {
			t.Fatalf("Classify(BREAKING CHANGE body) = %v, want BumpMajor", got)
		}
	})

	t.Run("feat: / feat(scope): drive minor", func(t *testing.T) {
		t.Parallel()
		for _, subject := range []string{
			"feat: add new endpoint",
			"feat(routing): support -target",
		} {
			got := ClassifyCommits([]CommitInfo{{Subject: subject}})
			if got != BumpMinor {
				t.Fatalf("Classify(%q) = %v, want BumpMinor", subject, got)
			}
		}
	})

	t.Run("non-feat non-breaking subjects drive patch", func(t *testing.T) {
		t.Parallel()
		for _, subject := range []string{
			"fix: nil pointer in resolver",
			"test: cover the edge case",
			"refactor: split helper",
			"docs: clarify the contract",
			"chore: bump dependency",
		} {
			got := ClassifyCommits([]CommitInfo{{Subject: subject}})
			if got != BumpPatch {
				t.Fatalf("Classify(%q) = %v, want BumpPatch", subject, got)
			}
		}
	})

	t.Run("empty input is BumpNone", func(t *testing.T) {
		t.Parallel()
		if got := ClassifyCommits(nil); got != BumpNone {
			t.Fatalf("Classify(nil) = %v, want BumpNone", got)
		}
	})

	t.Run("one breaking commit in a patch-only set still wins", func(t *testing.T) {
		t.Parallel()
		got := ClassifyCommits([]CommitInfo{
			{Subject: "fix: a"},
			{Subject: "fix: b"},
			{Subject: "feat!: c"},
			{Subject: "test: d"},
		})
		if got != BumpMajor {
			t.Fatalf("Classify mixed = %v, want BumpMajor", got)
		}
	})

	t.Run("feat among patches wins over patch", func(t *testing.T) {
		t.Parallel()
		got := ClassifyCommits([]CommitInfo{
			{Subject: "fix: a"},
			{Subject: "feat(routing): b"},
			{Subject: "docs: c"},
		})
		if got != BumpMinor {
			t.Fatalf("Classify(fix+feat+docs) = %v, want BumpMinor", got)
		}
	})

	t.Run("BREAKING-CHANGE hyphen variant also matches", func(t *testing.T) {
		t.Parallel()
		got := ClassifyCommits([]CommitInfo{{
			Subject: "refactor: tidy",
			Body:    "BREAKING-CHANGE: drop the old field",
		}})
		if got != BumpMajor {
			t.Fatalf("Classify(BREAKING-CHANGE) = %v, want BumpMajor", got)
		}
	})
}
