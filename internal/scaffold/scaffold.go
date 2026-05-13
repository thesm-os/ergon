// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package scaffold renders the starter file set `ergon init`
// writes into a fresh repository. Templates live under
// `templates/` and are embedded at build time so the binary
// carries everything it needs.
package scaffold

import (
	"embed"
	"errors"
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
}

// ErrTargetExists reports that a destination file already exists
// and would be overwritten. Run with --force to opt in.
var ErrTargetExists = errors.New("scaffold: destination file exists")

// Run renders every template under [templatesFS] into dest. Each
// template's filename loses the `.tmpl` suffix; directory
// structure is preserved. When force is false an existing target
// file causes [ErrTargetExists]; when true the file is
// overwritten.
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
		// Templates whose basename starts with `gitignore` rename to
		// `.gitignore`; embed.FS refuses dotfile names at the top of
		// the path, so we use a placeholder.
		base := filepath.Base(rel)
		if base == "gitignore" {
			rel = filepath.Join(filepath.Dir(rel), ".gitignore")
		}
		if base == "ergon.yaml" {
			rel = filepath.Join(filepath.Dir(rel), ".ergon.yaml")
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
				return fmt.Errorf("%w: %s", ErrTargetExists, rel)
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
