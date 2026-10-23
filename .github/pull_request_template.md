<!-- Say what the user now gets, in plain sentences. AGENTS.md explains each box. -->

## What changes

## Checklist

- [ ] Declared and observed status still never merge inside the exporter.
- [ ] No remote value (title, URL, error message, timestamp) became a label or reached a log field.
- [ ] The kustomize base and the Helm chart changed together (`make chart-sync`, `make chart-test`).
- [ ] Dashboards regenerated with `make dashboards` if the generator changed.
- [ ] `CHANGELOG.md` has an entry under `[Unreleased]`.
- [ ] The Datadog list changed only if a monitor needs the metric, in both places, and the README series table is updated.
- [ ] `make fmt-check lint vuln test test-race build rules-test chart-test` passes, and `make smoke` if rules, adapters, the cache or the collector changed. Name any check that did not run.
