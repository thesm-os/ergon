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

| Flag | Default | Meaning |
|---|---|---|
| `--fast` / `-f` | off | Stop at the first per-module or per-stage failure. Default runs every target and aggregates failures into a closing summary. |
| `--verbose` / `-v` | off | Stream the underlying tool's output live. Default buffers stdout+stderr per call and reveals the capture indented under the failing verdict on error. |
| `--config` | discovered | Path to `.ergon.yaml`. |

Stage filters apply to the umbrella commands `ergon lint` and `ergon
check`. Both accept `--only <names>` and `--skip <names>` (comma-
separated) on top of `lint.enabled` / `lint.disabled` and
`checks.enabled` / `checks.disabled` in `.ergon.yaml`. CLI `--only`
wins absolutely; `--skip` unions with the config denylist. Unknown
stage names surface as a usage error before any work runs.

### Command tree

| Group | Members |
|---|---|
| Top-level | `bootstrap`, `init`, `clean`, `fmt`, `license`, `generate`, `build`, `release` |
| `lint` | `vet`, `go`, `md`, `license` |
| `mod` | `list`, `install`, `tidy`, `verify` |
| `test` | `race`, `bench`, `fuzz`, `coverage` |
| `bench` | `baseline`, `regression` |
| `check` | `coverage`, `mutation`, `skip-expiry`, `error-prefix`, `commit-msg`, `vuln` |

The `check` umbrella runs `mod verify`, `lint`, `test`,
`coverage`, `skip-expiry`, `error-prefix`, and `vuln` in order.
Mutation testing is excluded from the umbrella because
`gremlins unleash` takes minutes per layer; invoke
`ergon check mutation` explicitly when running it.

## Scaffolding

```bash
ergon init            # write Makefile, .ergon.yaml, .gitignore, README, .github/workflows/ci.yml
ergon init --force    # overwrite existing files
```

Files that already exist are skipped with a notice, so
re-running fills in the gaps without overwriting local edits.

## Configuration

`.ergon.yaml` at the repository root configures every subsystem.
Every field is optional; values not set there fall back to each
subsystem's `Defaults()`. `ergon init` writes a worked example
showing every section populated.

| Key | Configures |
|---|---|
| `name` | Project identifier; drives the cache directory `.<name>/`. |
| `modules` | Override `go.work` discovery with an explicit list. |
| `bootstrap` | Extra `go install` targets on top of the built-in tool list, plus optional per-package version pins for deterministic CI installs. |
| `license` | go-license config path and walk excludes. |
| `lint` | Stage allow/denylist for `ergon lint` (`enabled` / `disabled`). |
| `markdown` | markdownlint-cli2 invocation (globs). |
| `test` | `go test` knobs: cpu, count, timeout, race-count, bench-count, fuzz-time. |
| `bench` | Baseline path and per-metric regression policy. |
| `checks.enabled` / `checks.disabled` | Stage allow/denylist for the `ergon check` umbrella. |
| `checks.excludes` | Shared path-exclusion list (coverage + mutation). |
| `checks.skips` | Shared structural-skip list (coverage + mutation). |
| `checks.coverage` | Per-layer line thresholds and the failing-function cap. |
| `checks.mutation` | Per-layer score / coverage thresholds and `gremlins` invocation policy. |
| `checks.error_prefix` | Target directories for the error-string-prefix check. |
| `checks.commit_msg` | Conventional-commit types and the max subject length. |

The repository's own [`.ergon.yaml`](.ergon.yaml) doubles as a
reference.

## Bench

```bash
ergon bench baseline      # pin the current benchmark numbers
ergon bench regression    # fail when a new run regresses against the pinned baseline
```

`bench regression` parses `benchstat -format csv` and applies a
different policy per metric:

| Metric | Default | Verdict |
|---|---|---|
| `sec/op` | ≥ 5% | FAIL (statistically significant delta only) |
| `allocs/op` | > 0% | FAIL (statistically significant positive delta) |
| `B/op` | ≥ 10% | WARN (advisory; never fails — too noisy under struct-padding changes) |

## Development

```bash
make bootstrap    # install tool dependencies
make check        # run the umbrella gate
make test         # go test ./...
make build        # go build ./cmd/ergon
```

The `Makefile` shells out to ergon (`ERGON ?= go run ./cmd/ergon`)
so the project's own gates run through the binary it ships.

## License

MIT. See [LICENSE](LICENSE).
