# Changelog

Notable changes, newest first. Versions follow [Semantic Versioning](https://semver.org/)
and are what `psp_exporter_build_info{version}` reports. The release workflow
publishes the section matching the tag as the release notes.

## [1.0.0] - 2026-09-21

First tagged release. It supersedes the untagged 0.4.0 and 0.5.0 builds that
ran on the NAS.

### Scope

- Declared status from seven adapters: `statuspage_v2`, `instatus_v1`,
  `hipay_v1`, `kener_v1`, `adyen_v1`, `paypal_v1` and `none`.
- Observed status through a Blackbox Exporter, collected per endpoint.
- A last-known-good cache with per-source and per-endpoint freshness.
  `/metrics` reads it and never does network I/O.
- Twenty-four metrics with explicit types, three of them counters that tell
  "we cannot measure" apart from "the provider is failing".
- Seven recording rules and seven alerts, among them
  `psp:unannounced_failure` and `psp:confirmed_incident`.
- A generated, tabbed Grafana dashboard.
- Deployment as a NAS compose stack or through kustomize, with a
  ServiceMonitor and a Datadog Autodiscovery check.
- An inventory of twenty-four providers, acquirers and banks: twenty-one
  declared sources and forty-eight probes.
- `pssst-check`, which qualifies a declared source before it enters the
  inventory.

### Fixed since 0.5.0

- PayPal: an event with an unrecognized or empty state or environment was
  dropped, leaving the provider operational. Only `closed` and `sandbox`
  events are skipped now; anything else counts as an incident of unknown
  severity.
- HiPay: a monitor list without its total stopped after the first page and
  could export a partial list as healthy. The total is now required, constant
  across pages and met exactly.
- Rules: a single failed poll removed a still-fresh signal and reset the timers
  of the paging alerts, so a source failing one poll in ten could never page.
  Correlation now depends on the age of the last success only.
- Rules: a failure inside an announced maintenance window paged as an
  unannounced failure. It now raises only the observed-failure warning.
- Datadog never collected the three counters. The OpenMetrics check expects
  counter names without `_total`, and the list now uses them.
- `deploy/compose.nas.yml` described a stack that could not be scraped. It
  now matches the one running on the NAS: images carrying their
  configuration, on `monitoring_default`, with no published port.
- The Kubernetes image tag comes from the kustomization instead of a
  hard-coded `pssst:0.5.0`.

### Changed

- The module path is `github.com/adeleglise/pssst`.
- `make build` and `make image` stamp the version from `git describe`.
- `make lint` adds staticcheck and a new `make vuln` runs govulncheck, both
  pinned as `go.mod` tools. CI runs them and the container smoke test.
- A `vX.Y.Z` tag publishes static binaries for linux/amd64, linux/arm64 and
  darwin/arm64 with their SHA-256 checksums.
- Dependabot proposes Go module, Action and base image updates weekly.
