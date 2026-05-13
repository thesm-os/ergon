# ergon

A CLI that runs lifecycle tasks for Go projects: format, lint,
test, benchmark, release. ergon reads `go.work` (or walks the
working tree for `go.mod` files) and runs each task against the
discovered module set.

The name is Greek ἔργον, "work, task."

## Install

```
go install go.thesmos.sh/ergon/cmd/ergon@latest
```

Prebuilt binaries for linux/darwin/windows × amd64/arm64 are
published to GitHub Releases.

## Usage

```
ergon bootstrap                 # install dev tools
ergon fmt
ergon lint
ergon test
ergon check                     # full pre-merge gate
ergon release -m "Release notes"
```

`ergon help <command>` lists per-command flags.

### Command tree

A bare verb runs the umbrella; named subcommands run individual
pieces.

| Group | Members |
|-------|---------|
| Top-level | `bootstrap`, `init`, `clean`, `fmt`, `license`, `generate`, `build`, `release` |
| `lint` | `vet`, `go`, `md`, `license` |
| `mod` | `list`, `install`, `tidy`, `verify` |
| `test` | `race`, `bench`, `fuzz`, `coverage` |
| `bench` | `baseline`, `regression` |
| `check` | `coverage`, `mutation`, `skip-expiry`, `error-prefix`, `commit-msg`, `vuln` |

## Configuration

ergon discovers defaults from `go.mod`, `go.work`, and a small
set of canonical filenames at the repository root
(`.go-license.yml`, `.markdownlint.yml`, `.golangci.yml`,
`.commitlintrc.yml`, `.coverage-config.yaml`). Override
discovered values with `.ergon.yaml` at the repository root.

## Scaffolding

```
ergon init                          # write Makefile, .ergon.yaml, workflows
ergon init --workflows reusable     # use the workflow_call variant
ergon init --check                  # read-only drift report
```

## Development

```
make bootstrap
make install
make check
make build
```

`make help` lists every target.

## License

MIT. See [LICENSE](LICENSE).
