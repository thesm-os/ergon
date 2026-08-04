# Contributing

## Getting set up

```bash
make bootstrap    # install tool dependencies
make check        # run the umbrella gate
make test         # go test ./...
make test-e2e     # run tests behind `//go:build e2e` (real git / network)
make build        # go build ./cmd/ergon
```

The `Makefile` shells out to ergon (`ERGON ?= go run ./cmd/ergon`)
so the project's own gates run through the binary it ships.

## Before you open a PR

Run `make check`. It is the same gate CI runs, so a green local
check means a green pipeline.

End-to-end tests live behind the `e2e` build tag so they stay
out of the default `go test ./...` (and `ergon check`) hot path.
The release pipeline carries one such test that spins up a
synthetic two-module git repo, runs `ApplyPipeline` against
real `git`, and asserts the bump-rewrite-commit-tag flow lands
correctly — the path with the highest silent-corruption risk
under fakeRunner-only coverage.

Run `make test-e2e` when you touch the release pipeline.

## Commit messages

Conventional Commits with subsystem scoping. The types and the
maximum subject length are enforced by `ergon check commit-msg`
and configured under `checks.commit_msg` in `.ergon.yaml`.

```
fix(release): resolve module path for nested submodules
build(release): mirror cask + attestation pipeline into scaffold templates
```

The body explains what the change entails and why it was made.

## Where design changes go

| Change | Needs |
|---|---|
| Bug fix, naming, implementation detail inside one package | The PR description |
| A load-bearing architectural decision | An [ADR](docs/adr/) |
| A new flag, stage, or config key | A PR, plus the [reference](docs/reference/) updated in the same change |

Documentation lives under [`docs/`](docs/README.md) and is
organised by reader state — reference, how-to, and explanation are
separate directories on purpose. Add to the one matching what your
change gives the reader.

## Security

Do not open a public issue for security-relevant findings. See
[SECURITY.md](SECURITY.md).
