# Scaffold a project

Lay down ergon's tooling in a repository that does not have it.

```bash
ergon init            # write Makefile, .ergon.yaml, .gitignore, README, .github/workflows/ci.yml
ergon init --force    # overwrite existing files
```

Files that already exist are skipped with a notice, so
re-running fills in the gaps without overwriting local edits.

That property makes `ergon init` safe to re-run after an upgrade:
it adds whatever the current version scaffolds and leaves your
edits alone. Reach for `--force` only when you want the generated
version back.

Next: tune the thresholds in
[`.ergon.yaml`](../reference/configuration.md), then add a release
pipeline with
[set up a release pipeline](set-up-a-release-pipeline.md).
