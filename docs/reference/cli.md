# CLI reference

Every flag and command group. For the reasoning behind the
umbrella split see [lint vs check](../explanation/lint-vs-check.md).

## Global flags

| Flag | Default | Meaning |
|---|---|---|
| `--fast` / `-f` | off | Stop at the first per-module or per-stage failure. Default runs every target and aggregates failures into a closing summary. |
| `--verbose` / `-v` | off | Stream the underlying tool's output live. Default buffers stdout+stderr per call and reveals the capture indented under the failing verdict on error. |
| `--config` | discovered | Path to `.ergon.yaml`. |

## Stage filters

Stage filters apply to the umbrella commands `ergon lint` and `ergon
check`. Both accept `--only <names>` and `--skip <names>` (comma-
separated) on top of `lint.enabled` / `lint.disabled` and
`checks.enabled` / `checks.disabled` in `.ergon.yaml`. CLI `--only`
wins absolutely; `--skip` unions with the config denylist. Unknown
stage names surface as a usage error before any work runs.

## Command tree

A bare verb runs the umbrella; named subcommands run individual
pieces. `ergon help <command>` lists per-command flags.

| Group | Members |
|---|---|
| Top-level | `bootstrap`, `init`, `clean`, `fmt`, `license`, `generate`, `build`, `release` |
| `lint` | `vet`, `go`, `md`, `license`, `skip-expiry`, `error-prefix`, `vuln` |
| `mod` | `list`, `install`, `tidy`, `verify` |
| `test` | `race`, `bench`, `fuzz`, `coverage` |
| `bench` | `baseline`, `regression` |
| `check` | `coverage`, `mutation`, `branch`, `commit-msg` |

## init

Writes `Makefile`, `.ergon.yaml`, `.gitignore`, `README.md`, and
`.github/workflows/ci.yml`. Existing files are skipped.

| Flag | Meaning |
|---|---|
| `--force` | Overwrite existing files instead of skipping them. |

Walkthrough: [scaffold a project](../how-to/scaffold-a-project.md).

## release init

| Flag | Repeatable | Meaning |
|---|---|---|
| `--upx` | no | Add binary packing to the pipeline. |
| `--homebrew <owner/repo>` | no | Publish a Homebrew cask to the named tap. |
| `--docker <registry-prefix>` | no | Publish one multi-arch image per binary, named `<prefix>/<binary>`. |
| `--main <path>` | yes | Name a build target. Defaults to `./cmd/<name>`. |
| `--force` | no | Overwrite existing files instead of skipping them. |

Walkthrough:
[set up a release pipeline](../how-to/set-up-a-release-pipeline.md).

## bench

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

Why `B/op` is advisory:
[benchmark regression policy](../explanation/benchmark-regression-policy.md).
