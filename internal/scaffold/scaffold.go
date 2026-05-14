// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package scaffold renders the starter file set `ergon init`
// writes into a fresh repository. Templates live under
// `templates/` and are embedded at build time so the binary
// carries everything it needs.
package scaffold

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed all:templates
var templatesFS embed.FS

// dotfileRenames maps the placeholder basenames used inside
// `templates/` to their dotfile equivalents written into the
// destination repository. embed.FS refuses dotfile names at the
// top of the embedded path, so each entry here trades a regular
// basename inside templates/ for the leading-dot name users
// actually expect in their repo root.
var dotfileRenames = map[string]string{
	"gitignore":              ".gitignore",
	"ergon.yaml":             ".ergon.yaml",
	"editorconfig":           ".editorconfig",
	"pre-commit-config.yaml": ".pre-commit-config.yaml",
	"golangci.yml":           ".golangci.yml",
	"go-license.yml":         ".go-license.yml",
	"markdownlint.yml":       ".markdownlint.yml",
}

// Vars carries the substitution values `text/template` injects
// into each scaffold template.
type Vars struct {
	// Name is the project identifier — drives the title of the
	// scaffolded README, the `name:` field of the generated
	// `.ergon.yaml`, and the `.<name>/` cache directory the
	// generated `.gitignore` excludes.
	Name string

	// Module is the Go module path the scaffolded repository
	// uses. Currently unused by the template set but reserved so
	// future templates (e.g. a starter `go.mod`) can consume it
	// without changing the public surface.
	Module string

	// Copyright is the holder string `.go-license.yml`'s header
	// template embeds (e.g. "Acme Inc."). Empty falls back to
	// [Vars.Name] so a freshly-scaffolded repo's license header
	// at least reads sensibly until the user edits it.
	Copyright string

	// License is the SPDX identifier the scaffolded
	// `.go-license.yml` declares (e.g. "MIT", "Apache-2.0").
	// Defaults to MIT when empty; the value is inserted verbatim
	// into the rendered header so users keep `.go-license.yml`'s
	// authority over license-header content.
	License string
}

// Run renders every template under [templatesFS] into dest. Each
// template's filename loses the `.tmpl` suffix; directory
// structure is preserved.
//
// Existing destination files are skipped (with a `skipped` notice
// on stdout) so re-running `ergon init` in a partially-initialised
// repository fills in only the gaps. Pass force=true to overwrite
// every target unconditionally.
func Run(stdout io.Writer, dest string, vars Vars, force bool) error {
	return fs.WalkDir(templatesFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel("templates", path)
		if err != nil {
			return err
		}
		rel = strings.TrimSuffix(rel, ".tmpl")
		// embed.FS refuses dotfile names at the top of the path, so
		// templates that resolve to repo-root dotfiles are stored
		// under a placeholder basename and renamed here. Add new
		// entries when adding scaffolded dotfiles.
		base := filepath.Base(rel)
		if newName, ok := dotfileRenames[base]; ok {
			rel = filepath.Join(filepath.Dir(rel), newName)
		}
		target := filepath.Join(dest, rel)

		body, err := fs.ReadFile(templatesFS, path)
		if err != nil {
			return fmt.Errorf("read template %s: %w", path, err)
		}
		rendered, err := render(string(body), vars)
		if err != nil {
			return fmt.Errorf("render %s: %w", path, err)
		}

		if !force {
			if _, statErr := os.Stat(target); statErr == nil {
				fmt.Fprintf(stdout, "skipped %s (exists; pass --force to overwrite)\n", rel)
				return nil
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, []byte(rendered), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		fmt.Fprintf(stdout, "wrote %s\n", rel)
		return nil
	})
}

// render parses body as a `text/template` and substitutes vars.
// Used per-file by [Run].
func render(body string, vars Vars) (string, error) {
	tmpl, err := template.New("ergon").Parse(body)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}
