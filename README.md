# PSSST

**P**ayment **S**tatus **S**ignals & **S**urveillance **T**ool: a Prometheus
exporter that answers one question about each payment provider you depend on.
Is it working?

It answers twice, on purpose:

- **Declared status** is what the provider says about itself, read from its own
  status page.
- **Observed status** is what its endpoints actually do, measured through a
  Blackbox Exporter.

The two never merge inside the exporter. Correlation is a Prometheus rule,
because the cases worth paging someone about are exactly the ones where the two
disagree:

| Declared | Observed | Meaning | Signal |
| --- | --- | --- | --- |
| healthy | healthy | Nothing to do. | none |
| incident | failing | Confirmed incident: both agree. | `psp:confirmed_incident` |
| healthy | failing | **Unannounced failure**: the provider has not noticed, or has not said. | `psp:unannounced_failure` |
| incident | healthy | The incident does not touch what you use, or is over. | `psp:declared_unavailable` |
| maintenance | failing | Planned work. Warn, do not page. | `PSPObservedUnavailable` only |
| absent or stale | any | One signal, not two. Silence is not health. | `PSPStatusSourceUnavailable` |

Releases are in [CHANGELOG.md](CHANGELOG.md); the evidence behind the last one
is in [docs/validation.md](docs/validation.md). Anyone changing the code, human
or agent, reads [AGENTS.md](AGENTS.md) first.

## Contents

- [How it works](#how-it-works)
- [Monitored providers](#monitored-providers)
- [Quick start](#quick-start)
- [How declared status is read](#how-declared-status-is-read)
- [Metrics](#metrics)
- [Datadog](#datadog)
- [Prometheus rules and alerts](#prometheus-rules-and-alerts)
- [Configuration](#configuration)
- [Deployment](#deployment)
- [Operations](#operations)
- [Adding a provider](#adding-a-provider)
- [Known limits](#known-limits)

## How it works

```text
   status pages ──▶ adapters (declared)  ─┐  one complete snapshot, or an error
                                          ├─▶ scheduler: one jittered loop per signal
   Blackbox /probe ──▶ client (observed) ─┘            │
                                                       ▼
                            cache: last known good + freshness + counters
                                                       │ read only, no I/O
                                                       ▼
                     Prometheus / Datadog ──▶ /metrics  /healthz  /readyz
```

- **A scrape never waits on a third party.** `/metrics` reads the in-memory
  cache. A provider timing out cannot slow a scrape.
- **A failure degrades one signal, never the endpoint.** One dead status page
  leaves every other series intact and exports its own staleness.
- **A failed poll keeps the last good observation** and updates health and
  freshness instead. A snapshot older than `stale_after` stops counting.

Protocol work (DNS, TCP, TLS, HTTP) is delegated to Blackbox Exporter rather
than reimplemented; see [ADR 0001](docs/adr/0001-blackbox.md).

## Monitored providers

[deploy/pssst.psp.yml](deploy/pssst.psp.yml) is the real inventory. Every URL
and component ID in it was resolved against the live page before being written
down. Each provider is probed twice, HTTPS reachability and a TLS handshake.

| Provider | Kind | Declared source | Mapped components | Probed host |
| --- | --- | --- | --- | --- |
| Payplug | psp | `statuspage_v2` | 3 | api.payplug.com |
| Payplug Enterprise | psp | `statuspage_v2` | overall only | api.payplug.com |
| Stripe | psp | `statuspage_v2` | 3 | api.stripe.com |
| Stancer | psp | `statuspage_v2` | 3 | api.stancer.com |
| SlimPay | psp | `statuspage_v2` | 2 | api.slimpay.net |
| Alma | psp | `statuspage_v2` | overall only | api.getalma.eu |
| GoCardless | psp | `statuspage_v2` | overall only | api.gocardless.com |
| Klarna | psp | `statuspage_v2` | 1 | api.klarna.com |
| Wise | psp | `statuspage_v2` | 2 | api.wise.com |
| SumUp | psp | `statuspage_v2` | overall only | api.sumup.com |
| Monext Payline | psp | `statuspage_v2` | overall only | services.payline.com |
| Lemonway | psp | `statuspage_v2` | overall only | api.lemonway.com |
| Fintecture | psp | `statuspage_v2` | 3 | api.fintecture.com |
| Treezor | bank | `statuspage_v2` | 3 | www.treezor.com (website) |
| Powens | bank | `statuspage_v2` | overall only | www.powens.com (website) |
| Mollie | psp | `instatus_v1` | 3 | api.mollie.com |
| Swan | bank | `instatus_v1` | 3 | api.swan.io |
| Adyen | psp | `adyen_v1` | overall only | checkoutshopper-live.adyen.com |
| PayPal | psp | `paypal_v1` | overall only | api-m.paypal.com |
| Qonto | bank | `kener_v1` | 4 | thirdparty.qonto.com |
| HiPay | psp | `hipay_v1` | 4 | secure-gateway.hipay-tpp.com |
| Checkout.com | psp | `none`: Statuspage behind SSO | none | api.checkout.com |
| Mangopay | psp | `none`: status inside the dashboard | none | api.mangopay.com |
| Bridge | psp | `none`: no public page found | none | api.bridgeapi.io |

Treezor and Powens serve their APIs on a customer-specific host, which is a
customer identifier and stays out of the inventory, so their observed signal
measures the public website, not the API.

## Quick start

Prerequisites: Go (the version in `go.mod`), Make, ripgrep, Podman or Docker
with a Compose provider, `promtool`, `curl`, `jq`.

```sh
make up                              # exporter + Blackbox + Prometheus + a fake provider
curl http://127.0.0.1:9099/metrics
open http://127.0.0.1:19090
make down
```

The demo is synthetic: it contacts no payment provider and binds every port to
loopback. The fake provider is driven by POST only, so the two signals can be
broken independently:

```sh
curl -X POST 'http://127.0.0.1:18080/control?incident=true'     # declared only
curl -X POST 'http://127.0.0.1:18080/control?api_failure=true'  # observed only
curl 'http://127.0.0.1:18080/control'                           # read state
```

Look at real sources without running the exporter:

```sh
make build
bin/pssst-check -type statuspage_v2 -url https://www.stripestatus.com -components 'api=2p0n66vlgnn2'
bin/pssst-check -config deploy/pssst.psp.yml     # audit every declared source
```

The audit prints one line per provider: whether the source answered, its
overall state, the mapped components that are down, active incidents by
severity and maintenance. Compare each line with the page a human sees; a
disagreement is a bug in an adapter or a mapping. The exit status is non-zero
when any source fails.

The full check, which CI runs too. Everything is offline except `vuln` and
`smoke`:

```sh
make fmt-check lint vuln test test-race build rules-test smoke
```

## How declared status is read

Every adapter returns **one complete snapshot or an error**: a partial
document fails the whole poll and shows up as a stale source, never as a
half-truth. Every adapter treats a **state it does not recognize as not
operational**. `overall` is false as soon as the page rollup or any published
component says so, and a mapped component missing from the page fails the
snapshot.

| Adapter | Reads | Active incident | Maintenance |
| --- | --- | --- | --- |
| `statuspage_v2` | `/api/v2/summary.json` | Any incident not `resolved` or `postmortem`. Impact gives the severity. | `scheduled`, or active while `in_progress` or `verifying`. An undated window still counts as scheduled. |
| `instatus_v1` | `/summary.json` and `/v2/components.json`, both required | Any incident not `RESOLVED`. The scale stops at `MAJOROUTAGE`, so `critical` never comes from here. | `NOTSTARTEDYET` or `INPROGRESS`. |
| `hipay_v1` | The monitor-list API behind the page; `base_url` is that full URL | None published: always zero. Only class `success` is operational. | None published: always zero. |
| `kener_v1` | The rendered page, current-state node only | None published: always zero. Only state `up` is operational. | None published: always zero. |
| `adyen_v1` | `/api/incident-messages/active` | Every active message. No component inventory. | None published: always zero. |
| `paypal_v1` | `/api/v1/events`, also covers Braintree | Every event except `closed` or `sandbox` ones, and except open production maintenance. | An open production window, active once its start has passed. |
| `none` | Nothing | Declared status is **absent**, which is not healthy. | |

Severities are normalized to `none`, `minor`, `major`, `critical` and
`unknown`. An incident of severity `none` is still an active, declared
incident. Remote identifiers reach logs only when they already match a safe
token shape; otherwise the incident is counted without its identifier.

## Metrics

Every metric has an explicit type. Labels come only from the configuration
(`psp`, `endpoint`, `component`) or from documented enums (`kind`, `severity`,
`outcome`, `version`). **Nothing remote ever becomes a label.**

| Metric | Type | Meaning |
| --- | --- | --- |
| `psp_exporter_build_info{version}` | gauge | Always 1. |
| `psp_info{psp,kind}` | gauge | Always 1. `kind` is `psp`, `acquirer` or `bank`; join on `psp` to filter. |
| `psp_status_source_configured{psp}` | gauge | 1 when a declared source is configured, 0 for `type: none`. |
| `psp_status_source_up{psp}` | gauge | The last poll produced a complete snapshot. |
| `psp_status_source_fresh{psp}` | gauge | The last good snapshot is younger than `stale_after`. |
| `psp_status_source_last_success_timestamp_seconds{psp}` | gauge | Unix seconds, 0 for never. |
| `psp_status_source_last_poll_timestamp_seconds{psp}` | gauge | Unix seconds, 0 for never. |
| `psp_status_source_stale_after_seconds{psp}` | gauge | Freshness threshold. |
| `psp_declared_operational{psp,component}` | gauge | Last good declared state. `overall` always exists. |
| `psp_active_incidents{psp,severity}` | gauge | Unresolved incidents per severity, all five always exported. |
| `psp_maintenance_active{psp}` | gauge | Windows open now. |
| `psp_maintenance_scheduled{psp}` | gauge | Windows announced, including undated ones. |
| `psp_maintenance_next_start_timestamp_seconds{psp}` | gauge | Earliest announced start, 0 when none. |
| `psp_probe_collection_up{psp,endpoint}` | gauge | The last attempt obtained a valid Blackbox response. |
| `psp_probe_fresh{psp,endpoint}` | gauge | The last valid Blackbox response is younger than `stale_after`. |
| `psp_probe_success{psp,endpoint}` | gauge | The last collected probe succeeded. Absent until a first collection. |
| `psp_probe_duration_seconds{psp,endpoint}` | gauge | Duration of the last collected probe. |
| `psp_probe_http_status_code{psp,endpoint}` | gauge | HTTP probes only; 0 means no response. |
| `psp_probe_ssl_earliest_cert_expiry_timestamp_seconds{psp,endpoint}` | gauge | TLS-capable probes only. |
| `psp_probe_last_poll_timestamp_seconds{psp,endpoint}` | gauge | Last completed attempt. |
| `psp_probe_last_collection_success_timestamp_seconds{psp,endpoint}` | gauge | Last valid Blackbox response. |
| `psp_probe_last_success_timestamp_seconds{psp,endpoint}` | gauge | Last successful probe. Stops while the endpoint fails. |
| `psp_probe_stale_after_seconds{psp,endpoint}` | gauge | Freshness threshold. |
| `psp_status_poll_total{psp,outcome}` | counter | Declared-source polls by outcome. |
| `psp_probe_collection_total{psp,endpoint,outcome}` | counter | Blackbox collection attempts by outcome. |
| `psp_probe_result_total{psp,endpoint,outcome}` | counter | Probe results, counted only when collection succeeded. |

The counters separate two failures a gauge cannot tell apart: a rising
`probe_collection_total{outcome="failure"}` means **we** cannot measure; a
rising `probe_result_total{outcome="failure"}` means **the provider** is
failing.

`stale_after` is `3 * (interval + jitter + timeout)` per signal. A timestamp
of 0 means "never", not "now"; the shipped rules check for it. The `*_fresh`
gauges apply the same test the rules do: they exist for consumers that cannot
compare a timestamp with the current time.

**Cardinality.** Per provider: 2 series, plus 16 and one per mapped component
when a declared source is configured, plus 12 to 14 per collected probe. The
shipped inventory exports about 1,070 series once every signal has answered.

## Datadog

Datadog bills custom metrics per series, so the Autodiscovery check in
[deployment.yaml](deploy/kubernetes/deployment.yaml) ships an **explicit,
minimal list**: about 300 series for the shipped inventory instead of about
1,070.

| Shipped | Series | Why |
| --- | --- | --- |
| `psp_declared_operational` | 55 | Declared state, `overall` plus mapped components. |
| `psp_active_incidents` | 105 | Declared incidents per severity. |
| `psp_maintenance_active` | 21 | Planned work is not an incident. |
| `psp_status_source_fresh` | 21 | Whether the declared values above are current. |
| `psp_probe_success` | 48 | Observed state. |
| `psp_probe_fresh` | 48 | Whether the observed value is current. |
| `psp_exporter_build_info` | 1 | Which version is running. |

**Why the fresh gauges.** Datadog metric queries have no `time()`, so the
timestamps cannot be compared with now, and the exporter keeps reporting the
last good value of a dead source. Without `*_fresh`, a source that stopped
answering would read as healthy forever. Always multiply a state by its
freshness.

**Why `tag_by_endpoint: false`.** By default the OpenMetrics check tags every
series with its scrape URL. That URL holds the pod IP, so every reschedule
creates a new set of series, and its `endpoint` tag collides with PSSST's own
`endpoint` label.

Opt-in extras, with their cost for the shipped inventory:

| Add | Series | For |
| --- | --- | --- |
| `psp_probe_duration_seconds` | 48 | Latency dashboards. |
| `psp_maintenance_scheduled` | 21 | Showing announced windows. |
| `psp_status_poll`, `psp_probe_collection`, `psp_probe_result` | 234 | Error rates of the collection itself. Counters are listed **without** `_total`: the check skips the suffixed name. |

The timestamp and threshold gauges are useless in Datadog for the reason
above. `TestDatadogListNamesRealMetrics` fails the build on a listed name the
exporter does not define.

Datadog runs none of the recording rules. These monitors reproduce them, with
the `pssst` namespace and a multi-alert on the grouping shown:

| Monitor | Queries | Formula, alert above 0 |
| --- | --- | --- |
| Observed failure, by `psp,endpoint` | `a = min:pssst.psp_probe_success{*}`, `b = min:pssst.psp_probe_fresh{*}` | `(1 - a) * b` |
| Declared problem, by `psp` | `a = min:pssst.psp_declared_operational{component:overall}`, `b = sum:pssst.psp_active_incidents{*}`, `c = min:pssst.psp_status_source_fresh{*}` | `clamp_max((1 - a) + b, 1) * c` |
| Unannounced failure, by `psp` | `a = min:pssst.psp_probe_success{*}`, `b = min:pssst.psp_probe_fresh{*}`, `c = min:pssst.psp_declared_operational{component:overall}`, `d = sum:pssst.psp_active_incidents{*}`, `e = sum:pssst.psp_maintenance_active{*}`, `f = min:pssst.psp_status_source_fresh{*}` | `(1 - a) * b * c * f * (1 - clamp_max(d + e, 1))` |

Keep the `for` periods of the Prometheus alerts as evaluation windows (5m, 10m
for unannounced). A provider with `type: none` has no declared series, so the
unannounced formula has no data for it, which is correct: nobody was listening.

## Prometheus rules and alerts

[deploy/prometheus/pssst.yml](deploy/prometheus/pssst.yml) holds seven
recording rules and seven alerts. `job` and `instance` survive every
aggregation, so two replicas never hide one another.

| Rule | Value |
| --- | --- |
| `psp:observed_unavailable` | 1 when any fresh endpoint fails, 0 when all are fresh and up, absent when partly unknown. |
| `psp:declared_unavailable` | 1 or 0 while a configured source is fresh; absent for stale and `type: none`. |
| `psp:confirmed_incident` | Both signals failing, unless a maintenance window is open with no incident declared. |
| `psp:unannounced_failure` | Observed failing, declared fresh and quiet, no maintenance window. |
| `psp:status_source_stale` | No fresh complete snapshot. |
| `psp:probe_collection_unavailable` | Blackbox cannot be reached or answers badly. |
| `psp:probe_collection_fresh` | Every endpoint has a fresh collection. |

Freshness is the age of the last success, never the last attempt: one failed
poll keeps the last observation and does not restart an alert timer. Only the
two source-health rules look at the attempt itself.

| Alert | `for` | Severity |
| --- | --- | --- |
| `PSPObservedUnavailable` | 5m | warning |
| `PSPConfirmedIncident` | 5m | critical |
| `PSPUnannouncedFailure` | 10m | critical |
| `PSPProbeCollectionUnavailable` | 5m | warning |
| `PSPStatusSourceUnavailable` | 10m | warning |
| `PSPCertificateExpiringSoon` (14 days) | 15m | warning |
| `PSPMaintenanceApproaching` (7 days) | 15m | info |

```sh
make rules-test    # promtool check plus fixtures
```

```promql
psp:unannounced_failure == 1
psp:confirmed_incident == 1
sum by (psp) (rate(psp_probe_collection_total{outcome="failure"}[5m]))
psp_info{kind="bank"}
```

## Configuration

One YAML document, strict: unknown and duplicate fields are rejected, the file
is capped at 1 MiB, and `${NAME}` expands in scalar values only, an unset name
being an error. Secrets belong in the environment, never on the command line.

| Field | Meaning and validation |
| --- | --- |
| `server.listen_address` | host:port, default `:9099`. |
| `polling.status_interval` | Declared-source interval, default `60s`, at most 24h. |
| `polling.probe_interval` | Blackbox interval, default `30s`. |
| `polling.timeout` | Request deadline, default `10s`, no greater than either interval. |
| `polling.jitter` | Startup and between-poll jitter, default `5s`, up to the smaller interval. |
| `blackbox.base_url` | Blackbox base URL; required as soon as one probe exists. |
| `blackbox.headers` | Optional bounded headers. |
| `psps[].id` | Metric identity: 1-64 chars of `A-Za-z0-9_.-`, starting alphanumeric or `_`. |
| `psps[].display_name` | Human-facing only, never a label. |
| `psps[].kind` | `psp`, `acquirer` or `bank`, default `psp`. |
| `psps[].status.type` | An adapter name, or `none`. |
| `psps[].status.base_url` | Required for every adapter. No credentials, query or fragment. Redirects are refused: use the canonical host. |
| `psps[].status.headers` | Optional bounded headers, normally an expanded token. |
| `psps[].status.components` | Local alias to upstream ID. The alias becomes a label; `overall` is reserved. Rejected for sources without components. |
| `psps[].probes[].id` | Endpoint ID, unique within the provider. |
| `psps[].probes[].module` | A Blackbox module name. |
| `psps[].probes[].target` | HTTP(S) URL or `host:port`, without credentials or fragment. |

Bounds: 128 providers, 32 probes each, 128 mapped components, 2 MiB per remote
response. Keep every Blackbox module timeout below `polling.timeout`, so
Blackbox can report a failed probe before the collection deadline.

The probe module `http_api_reachable` measures reachability, not
authorization: an API answering 401 or 404 on its root is up. A transport
failure, or a status outside the module's list (any 5xx, or a 429), is an
observed failure.

## Deployment

### Compose

[deploy/compose.nas.yml](deploy/compose.nas.yml) runs the exporter and its own
Blackbox, no Prometheus: an existing one scrapes it and owns retention, rules
and alerting. Both images carry their configuration, so an inventory change
ships as a new tag. The Dockerfile cross-compiles:

```sh
podman build --platform linux/amd64 --target exporter-nas --build-arg VERSION=X.Y.Z -t localhost/pssst-nas:X.Y.Z .
podman build --platform linux/amd64 --target blackbox-nas -t localhost/pssst-blackbox-nas:X.Y.Z .
```

Merge [pssst-scrape.yml](deploy/prometheus/pssst-scrape.yml) into the scrape
configuration and add [pssst.yml](deploy/prometheus/pssst.yml) to
`rule_files`. The stack joins the network Prometheus runs on and publishes no
port. The NAS procedure, build to rollback, is in
[AGENTS.md](AGENTS.md#the-nas).

### Kubernetes

```sh
kubectl kustomize deploy | kubectl apply -f -
```

No registry hosts the image: set `newName` in the kustomization's `images`
entry, or the pod stays in ImagePullBackOff. The inventory is generated into a
hashed ConfigMap, so an inventory change rolls the pods. Three collectors work
unchanged: `prometheus.io/*` annotations, a ServiceMonitor (drop it if the CRD
is absent) and the Datadog check above.

### Grafana

[deploy/grafana/](deploy/grafana/) holds a generated, tabbed dashboard: who is
down and who is in maintenance first, then declared status, probes and
collection health. Regenerate it, never hand-edit it.

## Operations

**Endpoints.** `/healthz` is liveness. `/readyz` succeeds once every source and
probe has completed a first attempt, failed or not, and turns false during
shutdown. `/metrics` only reads the cache. State is in memory: a restart
empties it until the first polls.

**Logging.** JSON on stderr, with `service` and `version` on every record.
Level from `-log-level` or `PSSST_LOG_LEVEL`; an unknown name falls back to
info.

| Level | Content |
| --- | --- |
| DEBUG | Poll start and end with `duration_ms`, and what the snapshot held. |
| INFO | Startup; declared incidents appearing or clearing. |
| WARN | A failed collection; a probe that Blackbox collected and reports failed. |
| ERROR | A cache update rejected by the inventory; a fatal startup problem. |

Logs carry error classes and safe incident IDs, never URLs, headers, bodies or
remote prose.

**Releases.** Pushing a `vX.Y.Z` tag reruns the checks, builds static binaries
with `make dist` and publishes them with `SHA256SUMS` and the matching
changelog section. `make dist` is reproducible from a clean checkout of the
tag.

```sh
gh release download v1.0.0 --repo adeleglise/pssst \
  --pattern 'pssst_1.0.0_linux_amd64' --pattern SHA256SUMS
shasum -a 256 --check --ignore-missing SHA256SUMS
```

**Security.** Responses are capped, redirects refused, URLs with credentials
rejected. The image runs as non-root on `scratch` with CA roots only; the
manifests drop every capability, forbid privilege escalation and mount a
read-only root filesystem.

## Adding a provider

1. Pick a short stable ID and stable endpoint and component IDs. Never a
   title, a URL or a customer identifier.
2. Find the machine-readable source. Try the vanity domain, then
   `<name>.statuspage.io`, then the page's own scripts: a client-rendered page
   often calls a public API.
3. Check the component names belong to the right company, then qualify the
   source with `pssst-check` and map only the components you need. Run
   `pssst-check -config` on the whole inventory afterwards.
4. Probe the production host the status page describes, with a reviewed
   Blackbox module.
5. Check freshness and label cardinality before enabling alerts on it.

A new adapter type is one entry in `internal/source`; see
[AGENTS.md](AGENTS.md#adding-an-adapter).

## Known limits

- Configuration is static; a change needs a restart. There is no history
  ingestion and no provider-specific business-health decision.
- `kener_v1` reads markup and is the weakest contract: an upstream redesign
  takes the source down, visible as a stale declared signal, never a wrong one.
- HiPay and Kener publish no incidents or maintenance, Adyen no maintenance;
  those series export 0.
- An open maintenance window mutes the unannounced signal for the whole
  provider, even when it covers an unrelated component.
- The Blackbox request carries no scrape-timeout header, so module timeouts
  must stay below `polling.timeout`.
- No image is published to a registry, and no NetworkPolicy ships with the
  Kubernetes manifests.
