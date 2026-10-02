# Changelog

Notable changes, newest first. Versions follow [Semantic Versioning](https://semver.org/)
and are what `psp_exporter_build_info{version}` reports. The release workflow
publishes the section matching the tag as the release notes.

## [Unreleased]

### Fixed

- Statuspage incidents in `postmortem` were counted as active incidents of
  unknown state. A postmortem follows resolution; it now counts as resolved.
- Datadog could not tell a current value from a last known good one: its
  queries have no `time()`, so a source that stopped answering read as healthy
  forever. See the two new gauges below.
- The Datadog check tagged every series with its scrape URL, which carries the
  pod IP, so each reschedule created a new set of billed series under an
  `endpoint` tag that collided with PSSST's own. `tag_by_endpoint` is now off.

### Added

- The exporter image is published to `ghcr.io/adeleglise/pssst` for
  linux/amd64 and linux/arm64: `edge` from `main`, `X.Y.Z`, `X.Y` and
  `latest` from release tags.
- `deploy/datadog/openmetrics.yaml`, the Datadog check for agents outside
  Kubernetes, kept identical to the pod annotation by a test.
- `deploy/compose.yml` runs PSSST next to any Prometheus in Docker, on the
  network named by `PROMETHEUS_NETWORK`, building from the checkout so a fork
  can deploy its own inventory as a GitOps stack.
- `psp_status_source_fresh{psp}` and `psp_probe_fresh{psp,endpoint}`: the
  rules' freshness test, computed per signal at scrape time.
- `pssst-check -config <inventory>` audits every declared source of an
  inventory in one run, one line per provider, and exits non-zero when any
  source fails.
- Tests for the HTTP client's bounds, the adapter registry, freshness, and a
  check that every name in the Datadog list is a real metric.

### Changed

- The Datadog list ships 7 metrics, about 300 series for the shipped
  inventory, instead of 24 metrics and about 1,070 series. The README lists
  the opt-in extras and their cost.
- Adapters are registered in one place, `internal/source`, which config
  validation, the scheduler and `pssst-check` all read. The component, incident
  and maintenance rules every adapter shared now live once in
  `internal/status`.
- The README and `AGENTS.md` are rewritten around the provider inventory, how
  each adapter decides status, and Datadog cost.

- The repository is public. The README documents installation, Kubernetes
  (a remote kustomize base with an inventory overlay), Docker Compose,
  Prometheus and Datadog for any deployment, and the kustomization pulls the
  published image.
- The Dockerfile targets `exporter-nas` and `blackbox-nas` are now
  `exporter-bundled` and `blackbox-bundled`.

### Removed

- The unused `internal/status/none` adapter. `none` has no adapter by design.
- `deploy/compose.nas.yml`, replaced by `deploy/compose.yml`, and the
  documentation of one private deployment: its operating procedure, its
  validation record and its planning notes.

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
  events are skipped now; anything else counts as an active incident.
- HiPay: a monitor list without its total stopped after the first page and
  could export a partial list as healthy. The total is now required, constant
  across pages and met exactly.
- Rules: a single failed poll removed a still-fresh signal and reset the timers
  of the paging alerts, so a source failing one poll in ten could never page.
  Correlation now depends on the age of the last success only.
- Rules: a failure inside an announced maintenance window paged as an
  unannounced failure, or as a confirmed incident where the page flags
  components under maintenance. Without a declared incident it now raises only
  the observed-failure warning.
- Adyen was probed on its test environment, which status.adyen.com does not
  describe. The probes now target `checkoutshopper-live.adyen.com`, the shared
  live host serving the web Drop-in, as `checkout_web_https` and
  `checkout_web_tls`.
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
