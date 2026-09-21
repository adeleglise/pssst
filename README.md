# PSSST

**P**ayment **S**tatus **S**ignals & **S**urveillance **T**ool. A Prometheus
exporter that answers one question about a payment provider: is it working?

It answers it twice, on purpose.

- **Declared status** is what the provider says about itself, read from its own
  status page.
- **Observed status** is what its endpoints actually do, measured by probing
  them through a Blackbox Exporter.

The two never merge inside the exporter. Correlating them is a Prometheus rule,
not a hidden decision in Go code, because the interesting cases are exactly the
ones where they disagree:

| Declared | Observed | What it means |
| --- | --- | --- |
| healthy | healthy | Nothing to do. |
| incident | failing | Confirmed incident. Both agree; trust it. |
| healthy | failing | **Unannounced failure.** The provider has not noticed, or has not said. |
| incident | healthy | The incident does not touch what you use, or is already over. |
| absent or stale | any | You have one signal, not two. Do not read silence as health. |

A tool that collapsed these into one number would throw away the only cases
worth paging someone about.

## Architecture

```text
                    ┌──────────────────────────────────────────┐
   status pages ───▶│ status adapters      (declared signal)   │
   (HTTPS, public)  │  statuspage_v2 instatus_v1 hipay_v1      │
                    │  kener_v1 adyen_v1 paypal_v1 none        │
                    └───────────────┬──────────────────────────┘
                                    │ Snapshot, or an error. Never half of one.
                    ┌───────────────▼──────────────────────────┐
                    │ scheduler: one jittered loop per signal  │
                    └───────────────┬──────────────────────────┘
                                    │
                    ┌───────────────▼──────────────────────────┐
   Blackbox ───────▶│ cache: last known good + freshness       │
   /probe           │  a failed poll updates health, never     │
                    │  erases the previous good observation    │
                    └───────────────┬──────────────────────────┘
                                    │ read-only
                    ┌───────────────▼──────────────────────────┐
   Prometheus ─────▶│ collector ──▶ /metrics                   │
   Datadog agent    │ /healthz  /readyz                        │
                    └──────────────────────────────────────────┘
```

Two properties hold by construction:

- **A scrape never waits on a third party.** `/metrics` reads the in-memory
  cache and does no network I/O. A provider timing out cannot slow a scrape.
- **A failure degrades one signal, not the endpoint.** One dead status page
  leaves every other series intact and exports its own staleness.

Protocol work is delegated to Blackbox Exporter rather than reimplemented; see
[ADR 0001](docs/adr/0001-blackbox.md).

## Quick start

Prerequisites: Go 1.27.1+, Make, ripgrep, Podman with a Compose provider,
`promtool`, `curl`, `jq`.

```sh
make up                              # exporter + Blackbox + Prometheus + a fake provider
curl http://127.0.0.1:9099/metrics
open http://127.0.0.1:19090
make down
```

The demo is fully synthetic: it contacts no payment provider and binds every
published port to loopback. The fake provider is driven by POST only, so a GET
can never mutate it:

```sh
curl -X POST 'http://127.0.0.1:18080/control?incident=true'     # declared only
curl -X POST 'http://127.0.0.1:18080/control?api_failure=true'  # observed only
curl 'http://127.0.0.1:18080/control'                           # read state
```

Driving them separately is the point: it reproduces the unannounced failure and
the confirmed incident on demand.

The full check, all of it offline except `vuln` and `smoke`. CI runs the same
targets:

```sh
make fmt-check lint vuln test test-race build rules-test smoke
```

## Configuration

One YAML document, strict: unknown and duplicate fields are rejected, the file
is capped at 1 MiB, and `${NAME}` expands in scalar values only, with an unset
reference being an error rather than an empty string. Secrets belong in the
environment or a secret manager, never in command-line arguments.

| Field | Meaning and validation |
| --- | --- |
| `server.listen_address` | host:port, default `:9099`. |
| `polling.status_interval` | Declared-source interval, default `60s`, positive, at most 24h. |
| `polling.probe_interval` | Blackbox collection interval, default `30s`. |
| `polling.timeout` | Shared request deadline, default `10s`, no greater than either interval. |
| `polling.jitter` | Startup and between-poll jitter, default `5s`, up to the smaller interval. |
| `blackbox.base_url` | Blackbox `/probe` base URL; required as soon as one probe exists. |
| `blackbox.headers` | Optional bounded request headers. |
| `psps[].id` | Stable metric identity. 1-64 chars of `A-Za-z0-9_.-`, starting alphanumeric or `_`. |
| `psps[].display_name` | Human-facing only. Never a metric label. |
| `psps[].kind` | `psp`, `acquirer` or `bank`. Default `psp`. Exported by `psp_info`, not added to other series. |
| `psps[].status.type` | An adapter name, or `none`. |
| `psps[].status.base_url` | Required for every adapter but `none`. No credentials, query or fragment. Redirects are refused, so use the canonical host. |
| `psps[].status.headers` | Optional bounded headers, normally an environment-expanded token. |
| `psps[].status.components` | Local alias to upstream component ID. The alias becomes a label and stays a strict token; the upstream key only has to match what the source publishes. `overall` is reserved. |
| `psps[].probes[].id` | Stable endpoint ID, unique within its provider. |
| `psps[].probes[].module` | A Blackbox module name. Blackbox owns module policy. |
| `psps[].probes[].target` | HTTP(S) URL or `host:port`, without credentials or fragment. |

Bounds: 128 providers, 32 probes each, 128 mapped components, 2 MiB per remote
response. Keep every Blackbox module timeout below `polling.timeout` so
Blackbox can return a failed probe before the collection deadline expires.

[deploy/pssst.psp.yml](deploy/pssst.psp.yml) is the real inventory:
twenty-four providers, acquirers and banks, twenty-one of them with a declared
source. Every URL and component ID in it was resolved against the live page
before being written down.

## Declared-status adapters

| Adapter | Source | Notes |
| --- | --- | --- |
| `statuspage_v2` | `/api/v2/summary.json` | Atlassian Statuspage. Current state only, not paginated, so no history is ingested. |
| `instatus_v1` | `/summary.json` + `/v2/components.json` | Instatus splits current state over two documents; both must succeed or the snapshot fails as a whole. Its impact scale stops at `MAJOROUTAGE`, so `critical` never originates there. |
| `hipay_v1` | monitor-list API | The page renders client side, so its HTML holds no state; `base_url` is the full monitor-list URL it calls, whose path carries the public page key. Paginated, and the walk is bounded. |
| `kener_v1` | the rendered page | Kener is open source but its API needs a key. The adapter parses the DOM and reads only the current-state node, never the daily history bars that reuse the same colour classes. Component keys are the displayed monitor names. |
| `adyen_v1` | `/api/incident-messages/active` | Active incidents only: Adyen publishes no component inventory and no machine-readable maintenance list, so those stay at zero rather than being guessed. |
| `paypal_v1` | `/api/v1/events` | Also covers Braintree. Only `production` events count. An open maintenance window is active or scheduled depending on its start date. |
| `none` | none | The provider publishes nothing machine-readable. Declared status is **absent**, which is not the same as healthy. |

Three providers currently have no declared source: Checkout.com hides its
Statuspage behind SSO, Mangopay keeps its service status inside an
authenticated dashboard, and no public status page was found for Bridge.

Every adapter follows the same two rules. **Unknown is never operational**: a
state the adapter does not recognize is reported as not operational, never
optimistically. And **a snapshot is atomic**: a partial document fails the whole
poll rather than producing a half-truth, which surfaces as a stale declared
signal instead of a wrong one.

Qualify a provider before adding it to a config:

```sh
make build
bin/pssst-check -type statuspage_v2 -url https://status.example.com
bin/pssst-check -type instatus_v1   -url https://status.example.com -components 'api=abc123'
```

It prints the exact snapshot the adapter would export, and nothing else
contacts the provider. A component ID the page does not publish fails the whole
snapshot on purpose, so check the mapping here first.

## Metrics

Every series carries an explicit type. A scraper that reads a counter as a
gauge loses every rate it could compute, and Datadog bills custom metrics per
series.

| Metric | Type | Meaning |
| --- | --- | --- |
| `psp_exporter_build_info{version}` | gauge | Build identity, always 1. |
| `psp_info{psp,kind}` | gauge | Entity class, always 1. Join on `psp` to filter by `psp`, `acquirer` or `bank`. |
| `psp_status_source_configured{psp}` | gauge | 1 only when a declared source is configured. |
| `psp_status_source_up{psp}` | gauge | The last poll produced a complete valid snapshot. |
| `psp_status_source_last_success_timestamp_seconds{psp}` | gauge | Unix seconds, or zero for never. |
| `psp_status_source_last_poll_timestamp_seconds{psp}` | gauge | Unix seconds, or zero for never. |
| `psp_status_source_stale_after_seconds{psp}` | gauge | Freshness threshold used by the rules. |
| `psp_declared_operational{psp,component}` | gauge | Last valid declared state. `overall` always exists for a valid snapshot. |
| `psp_active_incidents{psp,severity}` | gauge | Unresolved incidents per normalized severity. |
| `psp_maintenance_active{psp}` | gauge | Windows currently open. |
| `psp_maintenance_scheduled{psp}` | gauge | Windows announced, including undated ones. |
| `psp_maintenance_next_start_timestamp_seconds{psp}` | gauge | Earliest announced start, zero when none. |
| `psp_probe_collection_up{psp,endpoint}` | gauge | The last attempt obtained valid Blackbox exposition. |
| `psp_probe_success{psp,endpoint}` | gauge | The last collected probe succeeded. Absent until a first valid collection. |
| `psp_probe_duration_seconds{psp,endpoint}` | gauge | Duration of the last collected probe. |
| `psp_probe_http_status_code{psp,endpoint}` | gauge | Zero means no HTTP response. |
| `psp_probe_ssl_earliest_cert_expiry_timestamp_seconds{psp,endpoint}` | gauge | From TLS probes only. |
| `psp_probe_last_poll_timestamp_seconds{psp,endpoint}` | gauge | Last completed attempt. |
| `psp_probe_last_collection_success_timestamp_seconds{psp,endpoint}` | gauge | Last valid Blackbox response. |
| `psp_probe_last_success_timestamp_seconds{psp,endpoint}` | gauge | Last successful observed probe. |
| `psp_probe_stale_after_seconds{psp,endpoint}` | gauge | Freshness threshold used by the rules. |
| `psp_status_poll_total{psp,outcome}` | **counter** | Declared-source polls by outcome. |
| `psp_probe_collection_total{psp,endpoint,outcome}` | **counter** | Blackbox collection attempts by outcome. |
| `psp_probe_result_total{psp,endpoint,outcome}` | **counter** | Observed results, counted only when collection succeeded. |

The three counters exist to separate two failure modes a gauge cannot tell
apart. A rising `probe_collection_total{outcome="failure"}` means **we** cannot
measure. A rising `probe_result_total{outcome="failure"}` means **the provider**
is failing. Conflating them turns a monitoring outage into a false incident.

A zero timestamp means the event never happened. It does not mean "now", and
must never be compared against `time()` without first checking it is non-zero;
the shipped rules all do.

Labels are bounded by construction: `psp` and `endpoint` come from the config,
`severity` (`none`, `minor`, `major`, `critical`, `unknown`), `outcome`
(`success`, `failure`) and `kind` from documented enums, `component` from an
explicit mapping. **Nothing remote ever becomes a label**, no incident title,
no URL, no error message, no timestamp. Incident detail belongs in logs.

## Logging

Structured JSON on stderr, one object per line, with `service` and `version` on
every record so a shared index can tell two deployments apart. The level comes
from `-log-level` or `PSSST_LOG_LEVEL` (`debug`, `info`, `warn`, `error`); an
unknown name is reported and falls back to info rather than blocking startup.

| Level | Content |
| --- | --- |
| DEBUG | Poll start and completion with `duration_ms`, and what the snapshot held. |
| INFO | Startup with the resolved configuration; declared incidents appearing or clearing. |
| WARN | A failed collection; a probe Blackbox collected but reports as failed. |
| ERROR | A cache update rejected by the configured inventory; a fatal startup problem. |

A collected but failing probe is a WARN, not an ERROR, on purpose: that one is
the provider failing, not this service. Logs carry bounded error classes and
adapter-normalized incident IDs, never raw URLs, headers, response bodies or
remote prose.

## Endpoints and lifecycle

`/healthz` reports process liveness. `/readyz` succeeds only after every
configured source and probe has completed a first attempt; an upstream failure
still counts as an attempt. Readiness turns false during shutdown. `/metrics`
only reads the cache.

Each source and probe runs its own non-overlapping jittered loop. A later
invalid, timed-out, oversized, non-2xx or malformed retrieval **preserves the
last complete successful observation** and updates freshness instead. Staleness
is per source and per endpoint: `3 * (interval + jitter + timeout)`, exported so
rules never hard-code it. Cache state is in memory, so a restart discards
last-known-good until the workers poll again.

## Prometheus rules and alerts

[deploy/prometheus/pssst.yml](deploy/prometheus/pssst.yml) holds seven recording
rules and seven alerts. `job` and `instance` are preserved in every aggregation,
so two exporter replicas never hide one another.

Fresh means the last success is younger than `*_stale_after_seconds`. A failed
poll keeps the last observation until then, so it never restarts a correlation
alert timer. Only `psp:probe_collection_unavailable` and `psp:status_source_stale`
report the failed attempt itself, each through its own alert.

`psp:observed_unavailable` is 1 when any fresh collected endpoint fails, 0 only
when every endpoint has a fresh successful collection
(`psp:probe_collection_fresh`), and **absent** when the provider is partly
unknown. `psp:declared_unavailable` exists only while a configured source is
fresh. `psp:confirmed_incident` needs both; `psp:unannounced_failure` needs an
observed failure plus a fresh source reporting nothing, deliberately absent for
`type: none` and for stale sources, because you cannot call a failure
unannounced when nobody was listening. An open maintenance window is an
announcement, not an incident: a failure inside it is neither confirmed nor
unannounced, and only the observed-failure warning fires.

Alerts use sustained `for` periods so one bad poll never pages: observed failure
5m, confirmed incident 5m, unannounced failure 10m, collection failure 5m,
source unavailable 10m, TLS expiry within 14 days for 15m, maintenance within
seven days for 15m.

```sh
make rules-test    # promtool check + fixtures
```

```promql
psp:confirmed_incident == 1
psp:unannounced_failure == 1
sum by (psp) (rate(psp_probe_collection_total{outcome="failure"}[5m]))
psp_info{kind="bank"}
```

## Deployment

### Compose

[deploy/compose.nas.yml](deploy/compose.nas.yml) runs the exporter and its own
Blackbox, and no Prometheus: an existing instance scrapes it and owns
retention, rules and alerting. Build for the target architecture, the
Dockerfile cross-compiles rather than emulating:

```sh
podman build --platform linux/amd64 --target exporter-nas --build-arg VERSION=x.y.z -t pssst:x.y.z .
```

Merge [deploy/prometheus/pssst-scrape.yml](deploy/prometheus/pssst-scrape.yml)
into the existing scrape configuration and add
[deploy/prometheus/pssst.yml](deploy/prometheus/pssst.yml) to its `rule_files`.
Join the network Prometheus already runs on and it resolves the exporter by
service name, with no host port published.

### Kubernetes

```sh
kubectl kustomize deploy | kubectl apply -f -
```

The inventory is generated into a ConfigMap rather than copied, so the cluster
and the compose stack cannot drift, and the generated name carries a content
hash so an inventory change rolls the pods instead of leaving them on a stale
mount.

Liveness asks only whether the process still serves HTTP. Readiness waits for
that first full pass, so a rolling update never sends scrapes to an empty cache,
and the pass gets its own startup budget rather than being mistaken for a
liveness failure.

### Scraping

Three collectors work without changing anything: `prometheus.io/*` annotations
for a plain Prometheus, a ServiceMonitor for the Prometheus Operator (drop it
from the kustomization if the CRD is absent), and a Datadog Autodiscovery check.
The Datadog metric list is **explicit rather than a wildcard**: custom metrics
are billed per series, and a wildcard would ship every future series without
anyone deciding to. Adding a metric means adding it to that list.

### Grafana

[deploy/grafana/](deploy/grafana/) holds a generated, tabbed dashboard. The
first tab answers the two questions worth asking at a glance: who is down, and
who is in planned maintenance. The other three go deeper into declared status,
observed probes and collection health. It is generated, never hand-edited; see
that directory's README.

## Adding a provider

1. Pick a short stable ID, and stable endpoint and component IDs. Never a title,
   a URL or a customer identifier.
2. Find the machine-readable source. Try the vanity domain, then
   `<name>.statuspage.io`, then read the page's own scripts for the API it
   calls, a page that renders client side often has a public API behind it.
   Confirm the component names belong to the right company before trusting a
   page that merely answers.
3. Qualify it with `pssst-check`, then map only the components you need.
4. Choose a reviewed Blackbox module. Probe modules measure reachability, not
   authorization: an API answering 401 or 404 on its root is up.
5. Check `psp_status_source_configured`, freshness and label cardinality before
   enabling alerts on it.

## Adding an adapter

Implement `internal/status.StatusProvider`: return one complete
`status.Snapshot` or an error, never a partial one. Add the type to
`internal/config` validation, construct it in `internal/scheduler.New` and in
`cmd/pssst-check`, and document it in the adapter table above. Tests go against
local fixtures, never a live provider.

## Security

Use HTTPS and protected headers for upstream credentials, restrict access to
the exporter port, and mount configuration read-only. Responses are capped,
redirects refused, URLs rejected if they carry credentials. The image is a
multi-stage build running as non-root on `scratch` with CA roots only;
deployments drop all capabilities, forbid privilege escalation and use a
read-only root filesystem.

## Known limits

Configuration is static and requires a restart. Cache state is in memory. There
is no history ingestion, no hot reload and no PSP-specific business-health
decision. `kener_v1` is the only adapter reading markup and is the weakest
contract here: it fails loudly by design, so an upstream redesign takes that
source down and shows up as a stale declared signal rather than a wrong one.
Blackbox owns DNS, TCP and TLS behaviour, including its own module policy.
