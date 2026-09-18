# PSSST

PSSST (Payment Status Signals & Surveillance Tool) is a Prometheus exporter for payment-service-provider availability. It polls official Statuspage v2 summaries and a delegated Blackbox Exporter, stores the latest complete results, and exposes normalized metrics on `/metrics`.

Declared and observed signals remain separate. A PSP status page can be unavailable or stale while a payment API is healthy; an API can fail while its status page reports no incident. Prometheus rules correlate those facts only when the supporting data is fresh.

## Run locally with Podman

Prerequisites: Go 1.27.1 or newer, Make, ripgrep, Podman with a running machine and a Compose provider, `promtool`, `curl`, and `jq`.

Podman is the local runtime. The demo is fully synthetic: it calls no payment provider and binds every published port to loopback.

```sh
make rules-test
make up
curl http://127.0.0.1:9099/metrics
open http://127.0.0.1:19090
make down
```

`make up` starts `psp_exporter` on `127.0.0.1:9099`, Prometheus on `127.0.0.1:19090`, the fake PSP on `127.0.0.1:18080`, and Blackbox on `127.0.0.1:19115`. Blackbox is only needed for debugging; PSSST reaches it on the Compose network. Set `RUNTIME` to use a compatible OCI runtime command, for example `RUNTIME=podman make smoke`.

`make down` removes the demo containers and network while preserving its Prometheus data volume. The smoke test removes only its own disposable project and volume.

The full isolated check is:

```sh
make fmt-check lint test test-race build rules-test smoke
```

`smoke` uses separate loopback ports 29099/29090/28080/29115 and the Compose project `pssst-smoke`, recreates only that project, waits for Prometheus to scrape PSSST, changes the fake API and declared incident independently, checks raw metrics and recording rules through the Prometheus HTTP API, resets the fake server, and removes its containers and volume on exit.

The fake server is intentionally controllable only by POST:

```sh
curl -X POST 'http://127.0.0.1:18080/control?api_failure=true'
curl -X POST 'http://127.0.0.1:18080/control?incident=true'
curl -X POST 'http://127.0.0.1:18080/control?api_failure=false&incident=false'
curl http://127.0.0.1:18080/control  # read-only state summary
```

`api_failure` changes `/payment-api/health`; `incident` changes only `/api/v2/summary.json`. Query parameters on GET never mutate the fake state.

## Configure the exporter

Build local binaries with `make build`; this produces `bin/pssst` and `bin/fake-psp`. Run the exporter as `bin/pssst -config /secure/path/pssst.yml`; `-version` prints its build version. The container `VERSION` build argument sets build information (`podman build --build-arg VERSION=0.1.0 -t pssst:0.1.0 .`); local binaries default to `dev`. See [examples/pssst.production.yml](examples/pssst.production.yml) for a deliberately non-routable template and [examples/pssst.compose.yml](examples/pssst.compose.yml) for the demo.

The parser accepts exactly one YAML document, rejects unknown and duplicate fields, limits the file to 1 MiB, and expands `${NAME}` in scalar values only. An unset reference is an error. Put secrets in protected environment variables or a secret manager; do not put them in command-line arguments.

| Field | Meaning and validation |
| --- | --- |
| `server.listen_address` | HTTP host:port, default `:9099`. |
| `polling.status_interval` | Status-source interval, default `60s`, positive and at most 24h. |
| `polling.probe_interval` | Blackbox collection interval, default `30s`, positive and at most 24h. |
| `polling.timeout` | Shared request deadline, default `10s`; no greater than either interval. |
| `polling.jitter` | Startup and between-poll jitter, default `5s`; from zero through the smaller interval. |
| `blackbox.base_url` | HTTP(S) base URL for Blackbox `/probe`; required if any probe exists. |
| `blackbox.headers` | Optional, bounded request headers for Blackbox. |
| `psps[].id` | Stable, unique metric identifier. IDs are 1–64 alphanumeric, `_`, `.`, or `-` characters and start with an alphanumeric character or `_`. |
| `psps[].display_name` | Optional human-facing name; it is never a metric label. |
| `psps[].kind` | Entity class, one of `psp`, `acquirer`, `bank`. Defaults to `psp`. Exported by `psp_info`, never as a label on other metrics. |
| `psps[].status.type` | `statuspage_v2`, `instatus_v1`, `hipay_v1`, `kener_v1`, `adyen_v1`, `paypal_v1`, or `none`. `none` is unconfigured declared status, not a healthy source. |
| `psps[].status.base_url` | Required HTTP(S) status page base URL for any adapter but `none`; no credentials, query, or fragment. Redirects are refused, so use the canonical host. |
| `psps[].status.headers` | Optional bounded Statuspage headers, normally containing an environment-expanded secret. |
| `psps[].status.components` | Optional map from a stable local component ID to the upstream one. The local alias is what becomes a metric label and stays a strict token; the upstream key must match what the source publishes, which for `kener_v1` is the displayed label, spaces included. `overall` is reserved. Sources that publish no components reject the field rather than ignoring it. |
| `psps[].probes[].id` | Stable endpoint ID, unique within its PSP. |
| `psps[].probes[].module` | A configured Blackbox module identifier. PSSST validates its shape; Blackbox owns module policy. |
| `psps[].probes[].target` | HTTP(S) URL or `host:port`, without credentials or fragments. |

There can be at most 128 PSPs, 32 probes per PSP, and 128 mapped components. Headers reject unsafe hop-by-hop fields and control characters. URLs reject credentials and redirect following is disabled. Each remote response is capped at 2 MiB. Set every Blackbox module timeout below `polling.timeout` to allow Blackbox to return a failed-probe result before PSSST’s collection deadline. The demo uses a 1s module and a 2s collection timeout.

## Metric semantics

Every series carries an explicit type, because a scraper that reads a counter
as a gauge loses every rate it could compute, and Datadog bills custom metrics
per series.

| Shape | Type | Meaning |
| --- | --- | --- |
| `*_total{outcome}` | counter | Cumulative attempts. `outcome` is bounded to `success` and `failure`. |
| `*_timestamp_seconds` | gauge | Unix seconds. Zero means the event never happened, not "now". |
| `*_seconds` | gauge | A duration or an age in seconds. |
| boolean gauges | gauge | 1 or 0 only, never a third value. |
| `psp_info`, `psp_exporter_build_info` | gauge | Always 1; join on `psp` to filter by `kind`. |

Three counters separate the two failure modes that a gauge cannot tell apart:
`psp_status_poll_total` counts declared-source polls, `psp_probe_collection_total`
counts attempts to reach Blackbox, and `psp_probe_result_total` counts only the
probes Blackbox actually reported on. A rising collection failure rate means we
cannot measure; a rising result failure rate means the provider is failing.

Labels stay bounded by construction: `psp` and `endpoint` come from the
configuration, `severity` and `outcome` from documented enums, `component` from
an explicit mapping, `kind` from a three-value enum. Nothing remote ever becomes
a label.

## Logging

Structured JSON on stderr, one object per line, with `service` and `version` on
every record so a shared index can tell two deployments apart. The level is set
with `-log-level` or `PSSST_LOG_LEVEL` (`debug`, `info`, `warn`, `error`); an
unknown name is reported and falls back to info rather than preventing startup.

| Level | What goes there |
| --- | --- |
| DEBUG | Poll start and completion with `duration_ms`, and what the snapshot contained. |
| INFO | Startup with the resolved configuration, and declared incidents appearing or clearing. |
| WARN | A failed collection, and a probe that Blackbox collected but reports as failed. |
| ERROR | A cache update rejected by the configured inventory, and a fatal startup problem. |

A collected but failing probe is a WARN rather than an ERROR on purpose: that
one is the provider failing, not this service.

## Endpoints and lifecycle

`/healthz` reports process liveness. `/readyz` becomes successful only after every configured status worker and probe worker has completed its first attempt; an upstream failure still counts as an attempt. Readiness becomes false during shutdown. `/metrics` only reads the in-memory cache and never makes upstream I/O.

Each source and probe has an independent, non-overlapping polling loop. A later invalid, timed-out, oversized, non-2xx, or malformed retrieval preserves the last complete successful observation. A valid Blackbox response with `probe_success 0` is a collected observed failure, while a failed Blackbox request is collection failure.

Staleness is per source or endpoint: `3 * (interval + jitter + timeout)`. Before the first successful fetch, data is unknown. Statuspage `summary.json` is a current-state endpoint, so PSSST does not ingest historical pages or incident history.

## Declared-status adapters

| Adapter | Source | Notes |
| --- | --- | --- |
| `statuspage_v2` | `/api/v2/summary.json` | One current-state document. Not paginated: it carries unresolved incidents and upcoming maintenance only, so PSSST ingests no history. |
| `instatus_v1` | `/summary.json` and `/v2/components.json` | Instatus splits current state over two documents; both must succeed or the snapshot fails as a whole. Its impact scale stops at `MAJOROUTAGE`, so `critical` never originates from it. |
| `hipay_v1` | monitor-list API | The page renders client side, so its HTML carries no state; `base_url` is the full monitor-list URL it calls, whose path carries the public page key. Paginated, and the walk is bounded. It publishes no incident or maintenance list, so those stay at zero. |
| `kener_v1` | the rendered page | Kener is open source but its API needs a key, so a public page is only readable as markup. The adapter parses the DOM and reads only the current-state node, never the daily history bars that reuse the same colour classes. Component keys are the displayed monitor names. |
| `adyen_v1` | `/api/incident-messages/active` | Adyen's own API. Active incidents only: it publishes no component inventory and no machine-readable maintenance list, so those stay at zero. |
| `paypal_v1` | `/api/v1/events` | The API behind PayPal's status page, which also serves Braintree. Events carry a state, a type and an environment; only `production` events count. An open maintenance window is active or scheduled depending on its start date. |
| `none` | none | The provider publishes no machine-readable source. Declared status is absent, not healthy. |

`adyen_v1` and `paypal_v1` read provider-specific APIs that are not a public
contract and can change without notice. Both treat remote text as untrusted: an
incident whose identifier is not already a safe token is still counted, but
never reaches a log field.

The page adapters accept what real pages publish rather than an idealized schema:
an omitted empty incident list means nothing is active, and a scheduled
maintenance with no date still counts but cannot become the next start. A
missing `components` array or page indicator remains a failed retrieval.

Qualify a candidate provider before adding it to a configuration:

```sh
make build
bin/pssst-check -type statuspage_v2 -url https://status.example.com
bin/pssst-check -type instatus_v1 -url https://status.example.com -components 'payment_api=abc123'
```

It prints the normalized snapshot the adapter would export, and nothing else
contacts the provider. A component ID that the page does not publish fails the
whole snapshot on purpose, so check the mapping here first.

## Metrics

All metrics are gauges. Except for build information, they have a `psp` label; probe metrics also use `endpoint`, declared component metrics use `component`, and incidents use bounded `severity` (`none`, `minor`, `major`, `critical`, or `unknown`). `job` and `instance` are attached by Prometheus. PSSST never labels metrics with URLs, incident text, timestamps, credentials, or arbitrary remote values.

| Metric | Meaning |
| --- | --- |
| `psp_exporter_build_info{version}` | Build identity, always 1. |
| `psp_info{kind}` | Entity class, always 1. Join on `psp` to filter by `psp`, `acquirer` or `bank` without adding a label to every series. |
| `psp_status_source_configured` | 1 only for `statuspage_v2`. |
| `psp_status_source_up` | Last status request produced a complete valid snapshot. |
| `psp_status_source_last_success_timestamp_seconds` / `last_poll_timestamp_seconds` / `stale_after_seconds` | Status freshness timestamps and threshold. |
| `psp_declared_operational{component}` | Last valid declared state. `overall` always exists for a valid Statuspage snapshot; unknown states are non-operational. |
| `psp_active_incidents{severity}` | Count of unresolved incidents by normalized severity. |
| `psp_maintenance_active`, `psp_maintenance_scheduled`, `psp_maintenance_next_start_timestamp_seconds` | Current and upcoming maintenance; next start is zero when absent. |
| `psp_probe_collection_up` | Last attempt obtained valid Blackbox exposition. |
| `psp_probe_last_poll_timestamp_seconds`, `psp_probe_last_collection_success_timestamp_seconds`, `psp_probe_last_success_timestamp_seconds`, `psp_probe_stale_after_seconds` | Poll, collection, probe-success, and freshness state. |
| `psp_probe_success` | Last collected Blackbox `probe_success`; absent until a valid collection. |
| `psp_probe_duration_seconds`, `psp_probe_http_status_code`, `psp_probe_ssl_earliest_cert_expiry_timestamp_seconds` | Selected normalized Blackbox fields. HTTP/TLS fields are absent when the module has no value. |

## Prometheus rules and alerts

[deploy/prometheus/pssst.yml](deploy/prometheus/pssst.yml) contains recording states and alerts. It preserves `job`, `instance`, and `psp` in all PSP-level aggregations, so two exporter replicas never hide one another's signal.

`psp:probe_collection_unavailable` means one or more configured probes are unavailable, never collected, or stale. `psp:observed_unavailable` is 1 when any fresh collected endpoint fails, 0 only when every endpoint has fresh successful collection, and absent when the PSP is partly unknown. `psp:declared_unavailable` is present only when a configured official source is fresh; it is 1 for a non-operational overall state or any active incident, including `none` and `unknown` severity, and 0 when the source says no issue.

`psp:status_source_stale` covers an unavailable, never-successful, or stale configured status source. `psp:confirmed_incident` requires both fresh observed failure and declared failure. `psp:unannounced_failure` requires observed failure plus a fresh configured source reporting no issue. It is deliberately absent for `status.type: none` and for stale status sources.

Alerts apply a sustained `for` period: observed failure 5m, confirmed incident 5m, unannounced failure 10m, collection failure 5m, source unavailable 10m, TLS expiry within 14 days for 15m, and maintenance within seven days for 15m. The TLS alert also requires fresh valid collection. Validate the rules with `make rules-test`; fixtures cover confirmed and unannounced incidents, absent declared source, collection failure, stale data, mixed endpoint certainty, TLS, and maintenance.

Example queries:

```promql
psp:confirmed_incident
psp:unannounced_failure
psp:probe_collection_unavailable
psp_probe_duration_seconds{psp="example_statuspage",endpoint="payment_api"}
```

## Existing Prometheus and Grafana on the NAS

No NAS service is deployed or changed by this repository. To integrate PSSST with the existing Prometheus/Grafana installation at `192.168.1.250`, run PSSST on a host that the NAS can reach, copy [examples/prometheus-existing.yml](examples/prometheus-existing.yml) into the existing scrape configuration, and copy [deploy/prometheus/pssst.yml](deploy/prometheus/pssst.yml) into its rule directory. Add the rule file to the existing `rule_files` list, validate the merged configuration with that Prometheus's `promtool`, then use the existing reload/restart process. Replace `PSSST_HOST_OR_IP` with a routable address; `localhost` on the NAS is not the exporter host.

Import `deploy/grafana/pssst.json` into the existing Grafana and select its existing Prometheus data source. The repository does not create another Grafana service. The Portainer endpoint and its Keychain token are intentionally not used by PSSST or these instructions.

## Deploy on the NAS

[deploy/pssst.psp.yml](deploy/pssst.psp.yml) is the real inventory: twenty-four
providers, twenty-one of them with a declared source. Every URL and component ID
in it was resolved against the live page before being written down.

Several providers host an unbranded Statuspage with no vanity domain, reachable
only as `<name>.statuspage.io`: Fintecture, Treezor and Powens were found that
way. Check that pattern before concluding a provider publishes nothing, and
confirm the component names belong to the right company. Two lookalike pages
were rejected during this inventory.

Three providers remain without a declared source. Checkout.com hides its
Statuspage behind SSO, Mangopay keeps its service status inside an
authenticated dashboard, and no status page was found for Bridge at all. They
carry the observed signal alone.

`kener_v1` is the only adapter that reads markup, and it is the weakest
contract here. It is written to fail loudly: an upstream redesign takes the
source down and surfaces as a stale declared signal, which is the intended
outcome. A parser that guessed would be worse than no signal, because a false
declared state feeds the correlation rules.

A page that renders client side is worth a second look before giving up: its
HTML carries no state, but the API it calls may be public. HiPay was recovered
that way, by reading the endpoint out of the page's own script.

[deploy/compose.nas.yml](deploy/compose.nas.yml) runs the exporter and its own
Blackbox instance. It carries no Prometheus: the instance already running on the
NAS scrapes it and owns retention, rules and alerting.

```sh
podman build --platform linux/amd64 --target exporter -t pssst:VERSION .
podman compose -f deploy/compose.nas.yml up -d
```

The NAS runs x86_64, so build for `linux/amd64` from an arm64 workstation. The
Dockerfile cross-compiles instead of emulating.

Merge [deploy/prometheus/pssst-scrape.yml](deploy/prometheus/pssst-scrape.yml)
into the existing scrape configuration and add
[deploy/prometheus/pssst.yml](deploy/prometheus/pssst.yml) to its `rule_files`,
then reload. Publish the exporter on the NAS address rather than loopback:
Prometheus runs in a container and cannot reach the host loopback.

## Run on Kubernetes

[deploy/kustomization.yaml](deploy/kustomization.yaml) renders the whole stack:

```sh
kubectl kustomize deploy | kubectl apply -f -
```

The inventory is generated into a ConfigMap rather than copied, so the cluster
and the compose stack cannot drift, and the generated name carries a content
hash so an inventory change rolls the pods instead of leaving them on a stale
mount.

Liveness asks only whether the process still serves HTTP. Readiness waits for
every source and probe to have been tried once, so a rolling update never sends
scrapes to an empty cache, and the first pass gets its own startup budget rather
than being mistaken for a liveness failure.

The pod is scraped three ways without changing anything: `prometheus.io/*`
annotations for a plain Prometheus, a ServiceMonitor for the Prometheus
Operator (delete it from the kustomization if the CRD is absent), and a Datadog
Autodiscovery check. The Datadog metric list is explicit rather than a wildcard,
because custom metrics are billed per series and a wildcard would ship every
future series without anyone deciding to.

## Add a PSP or adapter

1. Choose a short stable PSP ID and stable endpoint/component IDs; never use titles, URLs, or customer identifiers as IDs.
2. Add a `statuspage_v2` source and only the upstream component IDs you need, or set `type: none` if no official status source exists.
3. Select a reviewed Blackbox module from [deploy/blackbox/blackbox.yml](deploy/blackbox/blackbox.yml). `http_2xx`, `tcp_connect`, `tls_connect`, and `dns` delegate protocol behavior to Blackbox. Set a DNS module's `query_name` to the PSP-owned name you intend to test and target the resolver.
4. Add the PSP to a protected production config, then check `psp_status_source_configured`, collection freshness, and low-cardinality labels before enabling alerts.

A future adapter implements `internal/status.StatusProvider`, returns one complete `status.Snapshot` or an error, and must preserve the same bounded, atomic behavior. Add its explicit type to `internal/config` validation and construct it in `internal/scheduler.New`; add local-server normalization and failure tests. Do not add a generic HTML scraper to this MVP.

## Operational security and limits

Use HTTPS and protected headers for upstream credentials, restrict access to the exporter port, and mount configuration read-only. Logs use bounded error classes and do not log raw URLs, headers, incident descriptions, response bodies, or configuration values. The OCI exporter image is a multi-stage build that runs as non-root on `scratch` with CA roots; Compose also uses read-only filesystems, drops capabilities, and forbids new privileges.

Static configuration needs a restart and cache state is in memory, so a restart discards last-known-good snapshots until workers poll again. There is no hot reload, historical incident ingestion, generic scraping, dashboard provisioning, authentication UI, arbitrary Blackbox metric forwarding, or PSP-specific business-health decision in PSSST. Blackbox owns DNS/TCP/TLS behavior and its module-level authentication policy.

The MVP adds collection-health/freshness gauges and `psp_maintenance_next_start_timestamp_seconds` to the supplied metric proposal; none of its metric names were removed or renamed. A `type: none` PSP emits `psp_status_source_configured=0` and probe metrics only. Zero timestamps mean no successful event yet. HTTP certificate expiry is mapped from Blackbox’s `probe_ssl_earliest_cert_expiry`.
