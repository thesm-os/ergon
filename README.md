# ergon

[![CI](https://github.com/thesm-os/ergon/actions/workflows/ci.yml/badge.svg)](https://github.com/thesm-os/ergon/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/thesm-os/ergon)](https://github.com/thesm-os/ergon/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/thesm-os/ergon)](go.mod)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A task runner for Go projects. ergon discovers modules from
`go.work` (or a filesystem walk for `go.mod` files) and runs each
task — format, lint, test, benchmark, release — against the
discovered set.

## Install

```bash
# Homebrew (macOS, Linux)
brew install thesm-os/tap/ergon

# Go toolchain
go install go.thesmos.sh/ergon/cmd/ergon@latest
```

Prebuilt binaries for `linux`, `darwin`, and `windows` on
`amd64` and `arm64` are attached to every
[GitHub Release](https://github.com/thesm-os/ergon/releases/latest)
along with cosign-signed checksums.

## Usage

A bare verb runs the umbrella; named subcommands run individual
pieces.

```bash
ergon bootstrap        # install dev tools (gofumpt, gci, golangci-lint, ...)
ergon fmt              # format Go + Markdown
ergon lint             # vet, golangci-lint, markdownlint, license verify
ergon test             # go test per module with coverage
ergon check            # pre-merge gate
ergon release -m "..." # bump versions and tag
```

`ergon help <command>` lists per-command flags.

### Global flags

See [CLI reference](docs/reference/cli.md#global-flags).

### Command tree

See [CLI reference](docs/reference/cli.md#command-tree), and
[why lint and check are separate umbrellas](docs/explanation/lint-vs-check.md).

## Scaffolding

See [scaffold a project](docs/how-to/scaffold-a-project.md).

### Release scaffolding

See [set up a release pipeline](docs/how-to/set-up-a-release-pipeline.md).

## Configuration

See [configuration reference](docs/reference/configuration.md).

## Bench

See [CLI reference](docs/reference/cli.md#bench), and
[why the benchmark policy differs per metric](docs/explanation/benchmark-regression-policy.md).

## Development

See [CONTRIBUTING](CONTRIBUTING.md).

## Documentation

[`docs/`](docs/README.md) — reference, how-to guides, explanation,
and architecture decision records.

## License

MIT. See [LICENSE](LICENSE).
