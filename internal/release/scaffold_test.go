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
)

// TestScaffold pins the contract of [Scaffold]: writes the
// pipeline files at the repo root plus per-module
// `internal/version/{version.go,version_test.go}` for every
// distinct [BuildSpec.ModulePath]; conditional sections in the
// goreleaser config activate when their flag is set; existing
// files are skipped without force and overwritten with force.
func TestScaffold(t *testing.T) {
	t.Parallel()

	t.Run("baseline writes pipeline + single version package", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name:   "proj",
			Builds: []BuildSpec{cmdBuild("proj", "example.com/proj")},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		assertFileExists(t, dest, ".goreleaser.yml")
		assertFileExists(t, dest, ".github/workflows/release.yml")
		assertFileExists(t, dest, "internal/version/version.go")
		assertFileExists(t, dest, "internal/version/version_test.go")

		// Goreleaser body must reference the build's module path
		// in the version-stamping ldflags.
		body := readAll(t, dest, ".goreleaser.yml")
		if !strings.Contains(body, "example.com/proj/internal/version.buildVersion") {
			t.Errorf("goreleaser.yml missing module-path ldflag:\n%s", body)
		}
		// Conditional blocks stay absent under baseline vars.
		for _, sentinel := range []string{"upx --best --lzma", "homebrew_casks:", "dockers:"} {
			if strings.Contains(body, sentinel) {
				t.Errorf("baseline goreleaser.yml contains %q; want absent", sentinel)
			}
		}
	})

	t.Run("UPX adds the post-build hook plus the apt install step", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name:   "proj",
			UPX:    true,
			Builds: []BuildSpec{cmdBuild("proj", "example.com/proj")},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		gor := readAll(t, dest, ".goreleaser.yml")
		if !strings.Contains(gor, "upx --best --lzma") {
			t.Errorf("UPX flag did not produce post-build hook:\n%s", gor)
		}
		wf := readAll(t, dest, ".github/workflows/release.yml")
		if !strings.Contains(wf, "apt-get install -y upx") {
			t.Errorf("UPX flag did not produce apt install step:\n%s", wf)
		}
	})

	t.Run("Homebrew adds the casks block plus HOMEBREW_TAP_TOKEN env", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name:             "proj",
			Homebrew:         true,
			HomebrewTapOwner: "myorg",
			HomebrewTapName:  "homebrew-tap",
			Builds:           []BuildSpec{cmdBuild("proj", "example.com/proj")},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		gor := readAll(t, dest, ".goreleaser.yml")
		if !strings.Contains(gor, "homebrew_casks:") {
			t.Errorf("Homebrew flag did not produce homebrew_casks block:\n%s", gor)
		}
		if !strings.Contains(gor, "owner: myorg") || !strings.Contains(gor, "name: homebrew-tap") {
			t.Errorf("homebrew owner/repo did not substitute:\n%s", gor)
		}
		wf := readAll(t, dest, ".github/workflows/release.yml")
		if !strings.Contains(wf, "HOMEBREW_TAP_TOKEN") {
			t.Errorf("Homebrew flag did not produce HOMEBREW_TAP_TOKEN env:\n%s", wf)
		}
	})

	t.Run("Docker adds the dockers_v2 block plus login step", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name:           "proj",
			Docker:         true,
			DockerPrefix:   "ghcr.io/myorg",
			DockerRegistry: "ghcr.io",
			Builds:         []BuildSpec{cmdBuild("proj", "example.com/proj")},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		gor := readAll(t, dest, ".goreleaser.yml")
		if !strings.Contains(gor, "dockers_v2:") {
			t.Errorf("Docker flag did not produce dockers_v2 block:\n%s", gor)
		}
		if !strings.Contains(gor, "- 'ghcr.io/myorg/proj'") {
			t.Errorf("docker image template did not substitute (want ghcr.io/myorg/proj):\n%s", gor)
		}
		if !strings.Contains(gor, "ids: [proj]") {
			t.Errorf("dockers_v2 entry missing ids link to build:\n%s", gor)
		}
		// Old syntax should NOT appear.
		if strings.Contains(gor, "docker_manifests:") {
			t.Errorf("legacy docker_manifests block leaked:\n%s", gor)
		}
		wf := readAll(t, dest, ".github/workflows/release.yml")
		if !strings.Contains(wf, "Login to ghcr.io") {
			t.Errorf("Docker flag did not produce docker-login step:\n%s", wf)
		}
		// ghcr.io path uses the workflow's GITHUB_TOKEN — no separate
		// DOCKER_USERNAME / DOCKER_TOKEN secrets required.
		if strings.Contains(wf, "DOCKER_USERNAME") {
			t.Errorf("ghcr.io login should not reference DOCKER_USERNAME:\n%s", wf)
		}
	})

	t.Run("Docker with multi-build emits one dockers_v2 entry per binary", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name:           "proj",
			Docker:         true,
			DockerPrefix:   "ghcr.io/myorg",
			DockerRegistry: "ghcr.io",
			Builds: []BuildSpec{
				cmdBuild("foo", "example.com/proj"),
				cmdBuild("bar", "example.com/proj"),
			},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		gor := readAll(t, dest, ".goreleaser.yml")
		for _, want := range []string{
			"- 'ghcr.io/myorg/foo'",
			"- 'ghcr.io/myorg/bar'",
			"ids: [foo]",
			"ids: [bar]",
		} {
			if !strings.Contains(gor, want) {
				t.Errorf("multi-binary docker config missing %q:\n%s", want, gor)
			}
		}
	})

	t.Run("non-ghcr docker registry references DOCKER_USERNAME / DOCKER_TOKEN", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name:           "proj",
			Docker:         true,
			DockerPrefix:   dockerHubRegistry + "/myorg",
			DockerRegistry: dockerHubRegistry,
			Builds:         []BuildSpec{cmdBuild("proj", "example.com/proj")},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		wf := readAll(t, dest, ".github/workflows/release.yml")
		if !strings.Contains(wf, "DOCKER_USERNAME") || !strings.Contains(wf, "DOCKER_TOKEN") {
			t.Errorf("non-ghcr docker login should reference DOCKER_USERNAME/DOCKER_TOKEN:\n%s", wf)
		}
	})

	t.Run("multi-build emits one builds entry per spec and dedupes version packages by module", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name: "proj",
			// Two builds, same module path -> one version package
			// at <root>/internal/version/.
			Builds: []BuildSpec{
				cmdBuild("foo", "example.com/proj"),
				cmdBuild("bar", "example.com/proj"),
			},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		gor := readAll(t, dest, ".goreleaser.yml")
		if !strings.Contains(gor, "id: foo") || !strings.Contains(gor, "id: bar") {
			t.Errorf("multi-build did not produce both entries:\n%s", gor)
		}
		// One version package at the shared module root.
		assertFileExists(t, dest, "internal/version/version.go")
	})

	t.Run("multi-module builds scaffold one version package per distinct module", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name: "proj",
			Builds: []BuildSpec{
				{ID: "foo", BinaryName: "foo", Dir: ".", MainPath: "./cmd/foo", ModulePath: "example.com/proj"},
				{ID: "cli", BinaryName: "cli", Dir: "./cli", MainPath: ".", ModulePath: "example.com/proj/cli"},
			},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		assertFileExists(t, dest, "internal/version/version.go")
		assertFileExists(t, dest, "cli/internal/version/version.go")

		// Each version package's docblock should reference its own
		// module path.
		root := readAll(t, dest, "internal/version/version.go")
		if !strings.Contains(root, "example.com/proj/internal/version") {
			t.Errorf("root version.go missing root ModulePath docref:\n%s", root)
		}
		cli := readAll(t, dest, "cli/internal/version/version.go")
		if !strings.Contains(cli, "example.com/proj/cli/internal/version") {
			t.Errorf("cli version.go missing cli ModulePath docref:\n%s", cli)
		}
	})

	t.Run("existing file is skipped without force; siblings still write", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		seedFile(t, dest, ".goreleaser.yml", "custom\n")
		var stdout strings.Builder
		vars := ScaffoldVars{
			Name:   "proj",
			Builds: []BuildSpec{cmdBuild("proj", "example.com/proj")},
		}
		if err := Scaffold(&stdout, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		if got := readAll(t, dest, ".goreleaser.yml"); got != "custom\n" {
			t.Errorf(".goreleaser.yml overwritten without force: %q", got)
		}
		assertFileExists(t, dest, ".github/workflows/release.yml")
		if !strings.Contains(stdout.String(), "skipped .goreleaser.yml") {
			t.Errorf("stdout = %q, want skip notice for .goreleaser.yml", stdout.String())
		}
	})

	t.Run("force overwrites an existing file", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		seedFile(t, dest, ".goreleaser.yml", "custom\n")
		vars := ScaffoldVars{
			Name:   "proj",
			Builds: []BuildSpec{cmdBuild("proj", "example.com/proj")},
		}
		if err := Scaffold(io.Discard, dest, vars, true); err != nil {
			t.Fatalf("Scaffold force err: %v", err)
		}
		body := readAll(t, dest, ".goreleaser.yml")
		if body == "custom\n" {
			t.Fatalf("force did not overwrite .goreleaser.yml")
		}
		if !strings.Contains(body, "project_name: proj") {
			t.Fatalf(".goreleaser.yml content unexpected after force overwrite:\n%s", body)
		}
	})

	t.Run("Name substitutes into the version package docblock", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		vars := ScaffoldVars{
			Name:   "mycli",
			Builds: []BuildSpec{cmdBuild("mycli", "example.com/proj")},
		}
		if err := Scaffold(io.Discard, dest, vars, false); err != nil {
			t.Fatalf("Scaffold err: %v", err)
		}
		body := readAll(t, dest, "internal/version/version.go")
		if !strings.Contains(body, "mycli --version") {
			t.Errorf("version.go docblock did not substitute Name:\n%s", body)
		}
	})

	t.Run("empty Builds returns an error", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		err := Scaffold(io.Discard, dest, ScaffoldVars{Name: "proj"}, false)
		if err == nil {
			t.Fatal("Scaffold returned nil for empty Builds, want error")
		}
	})
}

// TestParseDockerRegistry pins the registry-detection rule the
// cobra layer uses to populate [ScaffoldVars.DockerRegistry].
func TestParseDockerRegistry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		image string
		want  string
	}{
		{name: "ghcr.io is recognised as a registry", image: "ghcr.io/owner/foo", want: "ghcr.io"},
		{
			name:  "dotted hostname is recognised as a registry",
			image: "registry.example.com/foo", want: "registry.example.com",
		},
		{name: "localhost with port is recognised", image: "localhost:5000/foo", want: "localhost:5000"},
		{name: "bare localhost is recognised", image: "localhost/foo", want: "localhost"},
		{name: "docker.io as explicit prefix is recognised", image: "docker.io/owner/foo", want: dockerHubRegistry},
		{name: "no slash defaults to docker.io", image: "myimage", want: dockerHubRegistry},
		{name: "owner/repo without dotted prefix defaults to docker.io", image: "owner/foo", want: dockerHubRegistry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseDockerRegistry(tc.image); got != tc.want {
				t.Errorf("ParseDockerRegistry(%q) = %q, want %q", tc.image, got, tc.want)
			}
		})
	}
}

// TestResolveBuildSpec pins the upward-walk that locates each
// build's enclosing `go.mod`. Three documented layouts plus the
// no-go.mod failure path are exercised against a synthetic
// tempdir tree.
func TestResolveBuildSpec(t *testing.T) {
	t.Parallel()

	t.Run("root module: ./cmd/foo resolves to Dir=. with root ModulePath", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		seedFile(t, dest, "go.mod", "module example.com/proj\n\ngo 1.26\n")
		seedFile(t, dest, "cmd/foo/main.go", "package main\n")

		spec, err := ResolveBuildSpec(dest, "./cmd/foo")
		if err != nil {
			t.Fatalf("ResolveBuildSpec err: %v", err)
		}
		if spec.Dir != "." {
			t.Errorf("Dir = %q, want .", spec.Dir)
		}
		if spec.MainPath != "./cmd/foo" {
			t.Errorf("MainPath = %q, want ./cmd/foo", spec.MainPath)
		}
		if spec.ModulePath != "example.com/proj" {
			t.Errorf("ModulePath = %q, want example.com/proj", spec.ModulePath)
		}
		if spec.BinaryName != "foo" {
			t.Errorf("BinaryName = %q, want foo", spec.BinaryName)
		}
	})

	t.Run("submodule: ./cli resolves to Dir=./cli with cli ModulePath", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		seedFile(t, dest, "go.mod", "module example.com/proj\n\ngo 1.26\n")
		seedFile(t, dest, "cli/go.mod", "module example.com/proj/cli\n\ngo 1.26\n")
		seedFile(t, dest, "cli/main.go", "package main\n")

		spec, err := ResolveBuildSpec(dest, "./cli")
		if err != nil {
			t.Fatalf("ResolveBuildSpec err: %v", err)
		}
		if spec.Dir != "./cli" {
			t.Errorf("Dir = %q, want ./cli", spec.Dir)
		}
		if spec.MainPath != "." {
			t.Errorf("MainPath = %q, want .", spec.MainPath)
		}
		if spec.ModulePath != "example.com/proj/cli" {
			t.Errorf("ModulePath = %q, want example.com/proj/cli", spec.ModulePath)
		}
	})

	t.Run("nested submodule: ./cmd/foo with ./cmd/go.mod resolves to Dir=./cmd", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		seedFile(t, dest, "go.mod", "module example.com/proj\n\ngo 1.26\n")
		seedFile(t, dest, "cmd/go.mod", "module example.com/proj/cmd\n\ngo 1.26\n")
		seedFile(t, dest, "cmd/foo/main.go", "package main\n")

		spec, err := ResolveBuildSpec(dest, "./cmd/foo")
		if err != nil {
			t.Fatalf("ResolveBuildSpec err: %v", err)
		}
		if spec.Dir != "./cmd" {
			t.Errorf("Dir = %q, want ./cmd", spec.Dir)
		}
		if spec.MainPath != "./foo" {
			t.Errorf("MainPath = %q, want ./foo", spec.MainPath)
		}
		if spec.ModulePath != "example.com/proj/cmd" {
			t.Errorf("ModulePath = %q, want example.com/proj/cmd", spec.ModulePath)
		}
	})

	t.Run("no enclosing go.mod surfaces ErrNoEnclosingModule", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		seedFile(t, dest, "cmd/foo/main.go", "package main\n")

		_, err := ResolveBuildSpec(dest, "./cmd/foo")
		if err == nil {
			t.Fatal("ResolveBuildSpec returned nil, want ErrNoEnclosingModule")
		}
		if !errors.Is(err, ErrNoEnclosingModule) {
			t.Fatalf("err = %v, want wrapped ErrNoEnclosingModule", err)
		}
	})
}

// cmdBuild returns a [BuildSpec] for a build at ./cmd/<name>
// under the given module. Reduces inline noise in table-driven
// tests that need a build but do not care about its exact
// shape; callers needing a non-conventional layout (e.g. a
// submodule build with Dir=./cli) construct the [BuildSpec]
// inline.
func cmdBuild(name, modulePath string) BuildSpec {
	return BuildSpec{
		ID:         name,
		BinaryName: name,
		Dir:        ".",
		MainPath:   "./cmd/" + name,
		ModulePath: modulePath,
	}
}

// assertFileExists fails the test when <dest>/<rel> is not a
// regular file.
func assertFileExists(t *testing.T, dest, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
		t.Errorf("missing %s: %v", rel, err)
	}
}

// readAll returns the contents of <dest>/<rel> or fails the test
// on read failure.
func readAll(t *testing.T, dest, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dest, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}

// seedFile writes body to <dest>/<rel>, creating parent dirs as
// needed. Used by tests to pre-populate a tempdir before
// invoking [Scaffold] or [ResolveBuildSpec].
func seedFile(t *testing.T, dest, rel, body string) {
	t.Helper()
	full := filepath.Join(dest, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}
