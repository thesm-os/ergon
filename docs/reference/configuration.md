# Configuration reference

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
| `lint` | Stage allow/denylist for `ergon lint` (`enabled` / `disabled`) plus `error_prefix.target_dirs`. |
| `markdown` | markdownlint-cli2 invocation (globs). |
| `test` | `go test` knobs: cpu, count, timeout, race-count, bench-count, fuzz-time. |
| `bench` | Baseline path and per-metric regression policy. |
| `checks.enabled` / `checks.disabled` | Stage allow/denylist for the `ergon check` umbrella. |
| `checks.excludes` | Shared path-exclusion list (coverage + mutation). |
| `checks.skips` | Shared structural-skip list (coverage + mutation). |
| `checks.coverage` | Per-layer line thresholds and the failing-function cap. |
| `checks.mutation` | Per-layer score / coverage thresholds and `gremlins` invocation policy. |
| `checks.commit_msg` | Conventional-commit types and the max subject length. |

The repository's own [`.ergon.yaml`](../../.ergon.yaml) doubles as a
reference.
