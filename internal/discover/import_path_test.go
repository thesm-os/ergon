// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package discover

import (
	"os"
	"path/filepath"
	"testing"
)

// TestImportPath pins the parser's contract: the `module` line is
// recognised in canonical and quoted forms, leading whitespace is
// tolerated, missing go.mod and missing module directive each
// surface explicit errors.
func TestImportPath(t *testing.T) {
	t.Parallel()

	t.Run("returns the module directive value", func(t *testing.T) {
		t.Parallel()
		root := writeGoMod(t, "module go.thesmos.sh/ergon\n\ngo 1.26.3\n")
		got, err := ImportPath(root)
		if err != nil {
			t.Fatalf("ImportPath err: %v", err)
		}
		if got != "go.thesmos.sh/ergon" {
			t.Fatalf("got %q, want go.thesmos.sh/ergon", got)
		}
	})

	t.Run("strips surrounding quotes the parser permits", func(t *testing.T) {
		t.Parallel()
		root := writeGoMod(t, "module \"go.thesmos.sh/ergon\"\n")
		got, err := ImportPath(root)
		if err != nil {
			t.Fatalf("ImportPath err: %v", err)
		}
		if got != "go.thesmos.sh/ergon" {
			t.Fatalf("got %q, want go.thesmos.sh/ergon", got)
		}
	})

	t.Run("missing go.mod surfaces a read error", func(t *testing.T) {
		t.Parallel()
		_, err := ImportPath(t.TempDir())
		if err == nil {
			t.Fatalf("ImportPath returned nil for missing go.mod")
		}
	})

	t.Run("go.mod without module directive surfaces an error", func(t *testing.T) {
		t.Parallel()
		root := writeGoMod(t, "go 1.26\n")
		_, err := ImportPath(root)
		if err == nil {
			t.Fatalf("ImportPath returned nil for go.mod missing the module directive")
		}
	})
}

// writeGoMod writes body to <tempdir>/go.mod and returns the
// directory.
func writeGoMod(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}
