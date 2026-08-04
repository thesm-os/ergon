# Why lint and check are separate umbrellas

ergon groups its stages under two umbrellas rather than one. The
split is by *what a stage needs to run*, and it decides
which gates a contributor pays for on every commit.

The `lint` umbrella holds every static-analysis stage —
anything that examines source without running tests. The
`check` umbrella holds the test-derived gates (and orchestrates
`mod verify`, `lint`, `test`, `coverage` for the full pre-merge
pipeline). Mutation and branch are excluded from the default
`check` because each adds minutes per layer; both are appended
automatically when their `.ergon.yaml` thresholds are declared,
or invoked explicitly via `ergon check mutation` /
`ergon check branch`.

The consequence worth knowing: declaring a mutation or branch
threshold in `.ergon.yaml` silently lengthens `ergon check`. That
is deliberate — a threshold nobody enforces is decoration — but it
means the cost of the gate is set by configuration, not by the
command you typed.

See the [CLI reference](../reference/cli.md#command-tree) for the
full stage listing, and
[configuration](../reference/configuration.md) for the threshold
keys.
