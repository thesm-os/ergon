# Why the benchmark policy differs per metric

`ergon bench regression` fails on two metrics and only warns on a
third. The asymmetry is about signal quality, not about which
metric matters most.

`sec/op` and `allocs/op` fail because a statistically significant
move in either is almost always a real change in what the code
does. An allocation appears because someone added one; wall time
moves because work was added or removed.

`B/op` only warns. Bytes-per-operation shifts under changes that
alter nothing about the algorithm — reordering struct fields
changes padding, and a compiler upgrade can move it without a
single line of source changing. A gate that fails on that trains
contributors to re-run CI until it passes, which is worse than no
gate: it teaches the team that a red benchmark is noise, and the
next real regression is waved through the same way.

The thresholds themselves live in `.ergon.yaml` under `bench`;
see the [configuration reference](../reference/configuration.md).
The defaults are in the
[CLI reference](../reference/cli.md#bench).
