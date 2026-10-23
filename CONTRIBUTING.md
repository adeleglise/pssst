# Contributing

Read [AGENTS.md](AGENTS.md) first: it holds the one invariant that must never
break (declared and observed status never merge inside the exporter), the rules
that follow from it, and the lessons already paid for. It applies to humans and
agents alike.

Before opening a pull request, run the gate:

```sh
make fmt-check lint vuln test test-race build rules-test chart-test
make smoke
```

`rules-test`, `chart-test` and `smoke` need `promtool`, `helm` with `kustomize`
or `kubectl`, and a container engine. Say which check you could not run; CI runs
all of them. `make fuzz` gives the two hand-written parsers of untrusted input,
Blackbox exposition and Kener HTML, a short fuzz run.

Add a line to `CHANGELOG.md` under `[Unreleased]` for anything a user would
notice.
