// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package release

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

	"golang.org/x/mod/modfile"
)

// scaffoldTemplatesFS holds the embedded `ergon release init`
// template set. Two subtrees:
//
//   - `templates/pipeline/`  is rendered once at the repo root.
//     Holds `.goreleaser.yml` plus
//     `.github/workflows/release.yml`.
//   - `templates/versionpkg/` is rendered once per distinct Go
//     module across the builds. Holds `version.go` plus its
//     test, both at `<module-root>/internal/version/`.
//
// The `all:` prefix is required so embed.FS includes the
// `.github` subdirectory (entries starting with `.` are excluded
// by default).
//
//go:embed all:templates
var scaffoldTemplatesFS embed.FS

// ScaffoldVars carries the substitution values the release-init
// templates consume. The cobra layer (`ergon release init`)
// populates the fields from CLI flags and per-build `go.mod`
// reads.
//
// The templates use `<<` and `>>` as Go-template delimiters so
// goreleaser's own `{{ .Var }}` substitutions pass through
// unchanged. Field names appear verbatim in template directives
// (`<<.Name>>`, `<<.UPX>>`, ...).
type ScaffoldVars struct {
	// Name is the project identifier — used as goreleaser's
	// `project_name`, the homebrew cask name, and the binary
	// name when a build does not set its own.
	Name string

	// Builds enumerates the binaries goreleaser should produce.
	// One entry per `--main` the user passed to
	// `ergon release init` (defaults to `./cmd/<Name>` when no
	// flag is set). The cobra layer derives each entry's
	// [BuildSpec.Dir] / [BuildSpec.MainPath] / [BuildSpec.ModulePath]
	// from the nearest enclosing `go.mod` via [ResolveBuildSpec].
	Builds []BuildSpec

	// UPX, when true, emits the post-build UPX packing hook in
	// `.goreleaser.yml` and the apt install step in the workflow.
	// The hook filters to linux_* and windows_amd64; darwin is
	// skipped because Gatekeeper rejects UPX'd binaries
	// post-notarisation.
	UPX bool

	// Homebrew, when true, emits the `homebrew_casks` block in
	// `.goreleaser.yml` and the `HOMEBREW_TAP_TOKEN` env in the
	// workflow. HomebrewTapOwner and HomebrewTapName are the two
	// segments of the user-supplied `--homebrew owner/repo`.
	Homebrew         bool
	HomebrewTapOwner string
	HomebrewTapName  string

	// Docker, when true, emits the `dockers_v2` block in
	// `.goreleaser.yml` (one entry per build) and the buildx +
	// login steps in the workflow. DockerPrefix is the registry
	// path prefix the user supplied via `--docker` (e.g.
	// `ghcr.io/myorg`); each build's image template renders as
	// `<DockerPrefix>/<BuildSpec.BinaryName>` so a multi-binary
	// project produces one independent image per binary.
	// DockerRegistry is the registry portion (e.g. `ghcr.io`,
	// `docker.io`) derived from DockerPrefix by
	// [ParseDockerRegistry] so the workflow's login step can
	// target it directly.
	Docker         bool
	DockerPrefix   string
	DockerRegistry string
}

// BuildSpec describes one goreleaser `builds:` entry. Each entry
// names a binary that goreleaser produces, where its source
// lives, and which Go module to stamp via ldflags.
//
// Resolution: [ResolveBuildSpec] takes a user-supplied `--main`
// path (e.g. `./cmd/foo`, `./cli`) and walks upward from there
// to find the nearest enclosing `go.mod`. The directory holding
// that `go.mod` becomes [BuildSpec.Dir] (relative to the project
// root); the user's `--main` path relative to that directory
// becomes [BuildSpec.MainPath]; the `module` directive inside
// the `go.mod` becomes [BuildSpec.ModulePath].
//
// The three-field shape lets the goreleaser template handle the
// three layouts ergon supports out of the box:
//
//   - Root-module repo, command at `./cmd/foo`:
//     Dir=".", MainPath="./cmd/foo", ModulePath=<root module>.
//   - Submodule repo, command at `./cli` with `./cli/go.mod`:
//     Dir="./cli", MainPath=".", ModulePath=<cli module>.
//   - Submodule repo where `cmd/` is itself the submodule:
//     Dir="./cmd", MainPath="./foo", ModulePath=<cmd module>.
type BuildSpec struct {
	// ID is the goreleaser build identifier. Must be unique
	// across the `builds:` list. Defaults to [BuildSpec.BinaryName].
	ID string

	// BinaryName is the output binary name.
	BinaryName string

	// Dir is the goreleaser `dir:` — the working directory the
	// build runs from, relative to the project root. "." for
	// root-module builds; the submodule path for submodule
	// builds.
	Dir string

	// MainPath is the goreleaser `main:` — the main package path
	// relative to [BuildSpec.Dir]. "." when Dir is a submodule
	// containing a single main; "./cmd/foo" when Dir is "." and
	// the main lives in a subdirectory.
	MainPath string

	// ModulePath is the Go module path read from
	// `<Dir>/go.mod`. Feeds the
	// `-X <ModulePath>/internal/version.buildVersion=...` ldflag
	// so the resulting binary's `--version` returns stamped
	// values.
	ModulePath string
}

// scaffoldDotfileRenames maps placeholder basenames embedded
// under `templates/` to the dotfile names they should land as
// in the destination. embed.FS refuses dotfile names at the top
// of an embedded path, so the goreleaser template ships under
// the regular basename and gets renamed here.
var scaffoldDotfileRenames = map[string]string{
	"goreleaser.yml": ".goreleaser.yml",
}

// Scaffold renders the release-init template set into dest in
// two passes:
//
//  1. The pipeline templates (`.goreleaser.yml` plus the GitHub
//     Actions workflow) are written at the repo root.
//  2. The version-package templates (`version.go` plus the
//     accompanying test) are written at
//     `<dest>/<Dir>/internal/version/` for each distinct
//     [BuildSpec.ModulePath] across vars.Builds. Builds that
//     share a module path share one version package.
//
// Conditional blocks in the pipeline templates (UPX, Homebrew,
// Docker) activate based on the corresponding fields of vars.
//
// Existing files are skipped with a notice on stdout unless
// force is true, mirroring the `ergon init` ergonomic: re-
// running fills in only the gaps, never clobbers local edits.
func Scaffold(stdout io.Writer, dest string, vars ScaffoldVars, force bool) error {
	if len(vars.Builds) == 0 {
		return errors.New("release: Scaffold requires at least one BuildSpec in Vars.Builds")
	}
	if err := walkAndRender(stdout, dest, vars, force, "templates/pipeline", ""); err != nil {
		return err
	}
	seen := make(map[string]bool, len(vars.Builds))
	for _, b := range vars.Builds {
		if seen[b.ModulePath] {
			continue
		}
		seen[b.ModulePath] = true
		ctx := versionPkgContext{Name: vars.Name, ModulePath: b.ModulePath}
		// Build's Dir is relative to dest; the version package
		// lives at <Dir>/internal/version under each module.
		targetSubdir := filepath.Join(b.Dir, "internal", "version")
		if err := walkAndRender(stdout, dest, ctx, force, "templates/versionpkg", targetSubdir); err != nil {
			return err
		}
	}
	return nil
}

// versionPkgContext is the render context the version-package
// templates receive. Distinct from [ScaffoldVars] because each
// version package may sit under a different module — only
// [BuildSpec.ModulePath] of the build that owns the package is
// relevant inside the template.
type versionPkgContext struct {
	// Name is the project identifier — appears verbatim in the
	// version package's docblock ("read [Full] to populate
	// `<Name> --version`") so future readers know which CLI the
	// stamped values belong to.
	Name string

	// ModulePath is the Go module path the version package lives
	// under. Appears in the docblock as the import-path prefix
	// for the goreleaser ldflags.
	ModulePath string
}

// walkAndRender walks the embedded template tree under fsRoot,
// renders each `*.tmpl` file with ctx via [renderScaffold],
// applies [scaffoldDotfileRenames] to dotfile-equivalents, and
// writes the results under <dest>/<destSubdir>/. Common backbone
// shared by the pipeline pass and the per-module version-package
// pass.
//
// destSubdir is the destination prefix appended after dest
// (empty for the pipeline pass; `<Dir>/internal/version` for the
// version-package pass). The walker preserves the directory
// structure within fsRoot — so `templates/pipeline/.github/...`
// lands at `<dest>/.github/...` and `templates/versionpkg/x.go`
// lands at `<dest>/<destSubdir>/x.go`.
func walkAndRender(
	stdout io.Writer, dest string, ctx any, force bool,
	fsRoot, destSubdir string,
) error {
	return fs.WalkDir(scaffoldTemplatesFS, fsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(fsRoot, path)
		if err != nil {
			return err
		}
		rel = strings.TrimSuffix(rel, ".tmpl")
		base := filepath.Base(rel)
		if newName, ok := scaffoldDotfileRenames[base]; ok {
			rel = filepath.Join(filepath.Dir(rel), newName)
		}
		target := filepath.Join(dest, destSubdir, rel)

		body, err := fs.ReadFile(scaffoldTemplatesFS, path)
		if err != nil {
			return fmt.Errorf("read template %s: %w", path, err)
		}
		rendered, err := renderScaffold(string(body), ctx)
		if err != nil {
			return fmt.Errorf("render %s: %w", path, err)
		}

		if !force {
			if _, statErr := os.Stat(target); statErr == nil {
				fmt.Fprintf(stdout, "skipped %s (exists; pass --force to overwrite)\n",
					filepath.Join(destSubdir, rel))
				return nil
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, []byte(rendered), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		fmt.Fprintf(stdout, "wrote %s\n", filepath.Join(destSubdir, rel))
		return nil
	})
}

// renderScaffold parses body as a `text/template` with `<<` /
// `>>` delimiters (so goreleaser's own `{{ }}` directives pass
// through unchanged) and substitutes ctx. The delimiter choice
// matters: a release template would otherwise have to escape
// every goreleaser variable as `{{"{{"}} .Var {{"}}"}}`, which
// is unreadable.
func renderScaffold(body string, ctx any) (string, error) {
	tmpl, err := template.New("release-scaffold").Delims("<<", ">>").Parse(body)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// dockerHubRegistry is the fallback Docker Hub registry
// hostname. Extracted as a const because the value appears
// across both [ParseDockerRegistry]'s fallback and several
// test cases; goconst flags repeated literals here.
const dockerHubRegistry = "docker.io"

// ParseDockerRegistry extracts the registry portion of a
// Docker image reference. The first path segment is treated as
// a registry when it contains a `.` or `:` (so `ghcr.io`,
// `registry.example.com`, and `localhost:5000` all register
// correctly), or when it is the literal `localhost`; otherwise
// the reference is taken to be a Docker Hub repository and the
// registry defaults to `docker.io`.
//
// Used by the cobra layer (`ergon release init`) to drive the
// release-workflow template's docker-login step toward the
// right registry.
func ParseDockerRegistry(image string) string {
	if i := strings.Index(image, "/"); i > 0 {
		first := image[:i]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			return first
		}
	}
	return dockerHubRegistry
}

// ErrNoEnclosingModule is returned by [ResolveBuildSpec] when no
// `go.mod` is found on the path from the user-supplied --main
// down to the project root. Wrapping a sentinel lets the cobra
// layer surface a focused diagnostic ("create go.mod or pass
// --module to skip discovery").
var ErrNoEnclosingModule = errors.New("release: no enclosing go.mod found for build path")

// ResolveBuildSpec turns a user-supplied --main path (relative
// to dest) into a [BuildSpec] by walking upward from the path
// to find the nearest enclosing `go.mod`. The walk never escapes
// dest — paths that resolve outside the project root surface as
// an error so the user notices misconfigured input.
//
// Behaviour by layout:
//
//   - Root module + command at `./cmd/foo`:
//     starting at `<dest>/cmd/foo`, the walker climbs to
//     `<dest>/cmd` (no go.mod), then `<dest>/` (root go.mod).
//     Returns Dir=".", MainPath="./cmd/foo".
//   - Submodule at `./cli` with its own go.mod:
//     `<dest>/cli/go.mod` found on the first probe.
//     Returns Dir="./cli", MainPath=".".
//   - Nested submodule (`./cmd/go.mod`) with command at
//     `./cmd/foo`: walker climbs `<dest>/cmd/foo` (no go.mod) →
//     `<dest>/cmd/go.mod` found.
//     Returns Dir="./cmd", MainPath="./foo".
//
// The mainPath argument is taken as-is — it does not need to
// exist on disk (the user may be scaffolding ahead of writing
// the command). Resolution is purely syntactic across `go.mod`
// stat checks.
func ResolveBuildSpec(dest, mainPath string) (BuildSpec, error) {
	abs, err := filepath.Abs(filepath.Join(dest, mainPath))
	if err != nil {
		return BuildSpec{}, fmt.Errorf("release: resolve %q: %w", mainPath, err)
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return BuildSpec{}, fmt.Errorf("release: resolve dest: %w", err)
	}
	// The walker climbs from abs upward, stopping at absDest.
	// Each step asks "is there a go.mod here?"; the first hit is
	// the build's module root.
	cur := abs
	for {
		candidate := filepath.Join(cur, "go.mod")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			modPath, err := readScaffoldModulePath(candidate)
			if err != nil {
				return BuildSpec{}, err
			}
			dir, mainRel, err := relParts(absDest, cur, abs)
			if err != nil {
				return BuildSpec{}, err
			}
			return BuildSpec{
				ID:         filepath.Base(mainPath),
				BinaryName: filepath.Base(mainPath),
				Dir:        dir,
				MainPath:   mainRel,
				ModulePath: modPath,
			}, nil
		}
		if cur == absDest {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break // hit filesystem root before finding dest
		}
		cur = parent
	}
	return BuildSpec{}, fmt.Errorf("%w: %s", ErrNoEnclosingModule, mainPath)
}

// readScaffoldModulePath reads the `module` directive from goModPath via
// golang.org/x/mod/modfile and returns it. Wraps parse failures
// with the file path so the user sees which go.mod the loader
// rejected.
func readScaffoldModulePath(goModPath string) (string, error) {
	body, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("release: read %s: %w", goModPath, err)
	}
	parsed, err := modfile.Parse(goModPath, body, nil)
	if err != nil {
		return "", fmt.Errorf("release: parse %s: %w", goModPath, err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path == "" {
		return "", fmt.Errorf("release: %s has no module directive", goModPath)
	}
	return parsed.Module.Mod.Path, nil
}

// relParts converts (absDest, absModuleRoot, absMain) into the
// (Dir, MainPath) pair the goreleaser `dir:` / `main:` fields
// expect: both are repository-relative with a leading `./`
// prefix when the path is not exactly `.` (matching what users
// write by hand in goreleaser.yml).
func relParts(absDest, absModuleRoot, absMain string) (dir, mainPath string, err error) {
	dirRel, err := filepath.Rel(absDest, absModuleRoot)
	if err != nil {
		return "", "", fmt.Errorf("release: rel %s: %w", absModuleRoot, err)
	}
	mainRel, err := filepath.Rel(absModuleRoot, absMain)
	if err != nil {
		return "", "", fmt.Errorf("release: rel %s: %w", absMain, err)
	}
	return goReleaserPath(dirRel), goReleaserPath(mainRel), nil
}

// goReleaserPath returns a path in the shape goreleaser
// expects: "." stays bare; everything else is prefixed with
// `./` so the value reads naturally in YAML. Slashes are
// preserved (goreleaser config is always forward-slash regardless
// of host OS).
func goReleaserPath(p string) string {
	p = filepath.ToSlash(p)
	if p == "." {
		return "."
	}
	if strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") {
		return p
	}
	return "./" + p
}
