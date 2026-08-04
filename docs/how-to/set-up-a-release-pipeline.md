# Set up a release pipeline

Generate a goreleaser pipeline and its GitHub Actions workflow.

```bash
ergon release init                                  # baseline goreleaser + workflow
ergon release init --upx                            # + binary packing
ergon release init --homebrew owner/repo            # + Homebrew cask publishing
ergon release init --docker ghcr.io/owner           # + Docker image publishing (per-binary)
ergon release init --main ./cli                     # single binary at ./cli (auto-detected submodule)
ergon release init --main ./cmd/foo --main ./cmd/bar # multi-binary, two builds entries
```

`ergon release init` writes a starter `.goreleaser.yml`, a
matching `.github/workflows/release.yml`, and a per-module
`internal/version/{version.go,version_test.go}` (the package the
release pipeline's ldflags target). All conditional sections
activate via flags; existing files are skipped unless `--force`.

## Naming build targets

`--main` (repeatable) names each build target. The scaffold
walks each path upward to the nearest enclosing `go.mod`, so
root-module, submodule (`./cli/go.mod`), and nested-submodule
(`./cmd/go.mod`) layouts all generate the right `dir:` / `main:`
/ ldflag tuple. Defaults to `./cmd/<name>` when no `--main` is
passed.

## Publishing container images

`--docker` takes a registry prefix; each build's image template
renders as `<prefix>/<binary>`, so a multi-binary project
produces one independent multi-arch image per binary. ghcr.io
prefixes auto-wire the workflow's docker-login step to use
`GITHUB_TOKEN`; other registries reference `DOCKER_USERNAME` /
`DOCKER_TOKEN` repository secrets you supply yourself.

The full flag list is in the
[CLI reference](../reference/cli.md#release-init).
