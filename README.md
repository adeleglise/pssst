# PSSST

**P**ayment **S**tatus **S**ignals & **S**urveillance **T**ool: a Prometheus
exporter that tells you whether the payment providers you depend on are
working, and whether they have said so.

It reads two signals, on purpose, and never merges them:

- **Declared status** is what a provider says about itself, read from its
  public status page.
- **Observed status** is what its endpoints actually do, probed through a
  [Blackbox Exporter](https://github.com/prometheus/blackbox_exporter).

The interesting cases are exactly the ones where the two disagree, so the
correlation is a set of Prometheus rules you can read, not a hidden decision in
the exporter:

| Declared | Observed | Meaning | Signal |
| --- | --- | --- | --- |
| healthy | healthy | Nothing to do. | none |
| incident | failing | Confirmed incident: both agree. | `psp:confirmed_incident` |
| healthy | failing | **Unannounced failure**: the provider has not noticed, or has not said. | `psp:unannounced_failure` |
| incident | healthy | The incident does not touch what you use, or is over. | `psp:declared_unavailable` |
| maintenance | failing | Planned work. Warn, do not page. | `PSPObservedUnavailable` |
| absent or stale | any | One signal, not two. Silence is not health. | `PSPStatusSourceUnavailable` |

It ships with an inventory of 24 European payment providers, acquirers and
banks (Stripe, Adyen, PayPal, Mollie, Klarna, GoCardless and more), and adding
your own is a few lines of YAML.

## Contents

- [Quick start](#quick-start)
- [Install](#install)
- [Configure your providers](#configure-your-providers)
- [Deploy on Kubernetes](#deploy-on-kubernetes)
- [Deploy with Docker Compose](#deploy-with-docker-compose)
- [Prometheus](#prometheus)
- [Datadog](#datadog)
- [Grafana](#grafana)
- [Metrics reference](#metrics-reference)
- [How declared status is read](#how-declared-status-is-read)
- [Default inventory](#default-inventory)
- [Operations](#operations)
- [Development](#development)
- [Known limits](#known-limits)
- [License](#license)

## Quick start

**See it work, offline.** The demo runs the exporter, a Blackbox Exporter, a
Prometheus with the rules loaded and a fake provider, all bound to loopback:

```sh
git clone https://github.com/adeleglise/pssst && cd pssst
docker compose up --build -d      # or: make up (Podman by default)
curl -s http://127.0.0.1:9099/metrics | grep '^psp_'
open http://127.0.0.1:19090       # Prometheus
```

Break the two signals independently and watch the rules react:

```sh
curl -X POST 'http://127.0.0.1:18080/control?api_failure=true'  # observed only: unannounced failure
curl -X POST 'http://127.0.0.1:18080/control?incident=true'     # declared too: confirmed incident
docker compose down
```

**Check the real providers.** `pssst-check` reads every declared source of an
inventory once and prints what the exporter would export, one line per
provider:

```sh
docker run --rm --entrypoint /pssst-check \
  -v "$PWD/deploy/pssst.psp.yml:/etc/pssst/config.yml:ro" \
  ghcr.io/adeleglise/pssst:edge -config /etc/pssst/config.yml
```

```text
PSP     TYPE           RESULT  OVERALL      COMPONENTS DOWN  INCIDENTS  MAINTENANCE
stripe  statuspage_v2  ok      operational  none             none       0 active, 0 scheduled
...
```

## Install

| Form | Where |
| --- | --- |
| Container image | `ghcr.io/adeleglise/pssst`, linux/amd64 and linux/arm64. `X.Y.Z` per release, `latest`, and `edge` for `main`. Runs as non-root on `scratch`; contains `/pssst` and `/pssst-check`, no inventory. |
| Binaries | [Releases](https://github.com/adeleglise/pssst/releases): `pssst` and `pssst-check` for linux/amd64, linux/arm64 and darwin/arm64, with `SHA256SUMS`. |
| Source | `make build` with the Go version in `go.mod`. |

The exporter needs a Blackbox Exporter to probe through. Use the modules in
[deploy/blackbox/blackbox.yml](deploy/blackbox/blackbox.yml), or reuse an
existing instance that has equivalent ones.

```sh
pssst -config pssst.yml            # serves :9099 by default
pssst -version
```

## Configure your providers

One YAML file. Start from [deploy/pssst.psp.yml](deploy/pssst.psp.yml), the
default inventory, or from the minimal
[examples/pssst.production.yml](examples/pssst.production.yml):

```yaml
polling:
  status_interval: 300s   # status pages change slowly; poll them gently
  probe_interval: 60s
  timeout: 15s
  jitter: 30s

blackbox:
  base_url: "http://pssst-blackbox:9115"

psps:
  - id: stripe                       # becomes the psp label
    display_name: Stripe
    kind: psp                        # psp, acquirer or bank
    status:
      type: statuspage_v2
      base_url: "https://www.stripestatus.com"
      components:                    # optional: local alias -> upstream ID
        api: 2p0n66vlgnn2
    probes:
      - id: api_https
        module: http_api_reachable   # a module in the Blackbox configuration
        target: "https://api.stripe.com/"
      - id: api_tls
        module: tls_connect
        target: "api.stripe.com:443"

  - id: some_bank
    status:
      type: none                     # publishes nothing: declared status is absent
    probes:
      - id: api_tls
        module: tls_connect
        target: "api.some-bank.example:443"
```

| Field | Meaning and validation |
| --- | --- |
| `server.listen_address` | host:port, default `:9099`. |
| `polling.status_interval` | Declared-source interval, default `60s`, at most 24h. |
| `polling.probe_interval` | Blackbox interval, default `30s`. |
| `polling.timeout` | Request deadline, default `10s`, no greater than either interval. |
| `polling.jitter` | Startup and between-poll jitter, default `5s`, up to the smaller interval. |
| `blackbox.base_url` | Blackbox base URL; required as soon as one probe exists. |
| `blackbox.headers` | Optional bounded headers. |
| `psps[].id` | 1-64 chars of `A-Za-z0-9_.-`, starting alphanumeric or `_`. |
| `psps[].display_name` | Human-facing only, never a label. |
| `psps[].kind` | `psp`, `acquirer` or `bank`, default `psp`. |
| `psps[].status.type` | An adapter (below), or `none`. |
| `psps[].status.base_url` | Required for every adapter. No credentials, query or fragment. Redirects are refused: use the canonical host. |
| `psps[].status.headers` | Optional bounded headers. |
| `psps[].status.components` | Local alias to upstream ID. The alias becomes a label; `overall` is reserved. Rejected for sources without components. |
| `psps[].probes[].id` | Endpoint ID, unique within the provider. |
| `psps[].probes[].module` | A Blackbox module name. |
| `psps[].probes[].target` | HTTP(S) URL or `host:port`, without credentials or fragment. |

The file is strict: unknown and duplicate fields are rejected and it is capped
at 1 MiB. `${NAME}` expands from the environment in values, an unset name
being an error, so tokens stay out of the file. Bounds: 128 providers, 32
probes each, 128 mapped components, 2 MiB per remote response. Keep every
Blackbox module timeout below `polling.timeout`.

**Adding a provider.**

1. Find its machine-readable status source. Try the vanity domain, then
   `<name>.statuspage.io`, then the page's own scripts: a client-rendered page
   often calls a public API. Check the component names belong to the right
   company: a page that answers is not always the right page.
2. Qualify it: `pssst-check -type statuspage_v2 -url https://status.example.com`
   prints the snapshot, including every component ID you can map.
3. Probe the production host the status page describes. Never put a
   customer-specific hostname or identifier in the inventory.
4. Run `pssst-check -config` on the whole file before deploying it.

`http_api_reachable` measures reachability, not authorization: an API answering
401 or 404 on its root is up. A transport failure, or a status outside the
module's list (any 5xx, or a 429), is an observed failure.

## Deploy on Kubernetes

[deploy/](deploy/) is a kustomize base. Use it as a remote base, and replace the
inventory with yours in an overlay:

```yaml
# kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: monitoring
resources:
  - https://github.com/adeleglise/pssst//deploy?ref=v1.1.0
configMapGenerator:
  - name: pssst-config
    behavior: replace
    files:
      - config.yml=inventory.yml     # your inventory, next to this file
images:
  - name: pssst
    newName: ghcr.io/adeleglise/pssst
    newTag: "1.1.0"
# Without the Prometheus Operator, drop the ServiceMonitor:
# patches:
#   - patch: |-
#       $patch: delete
#       apiVersion: monitoring.coreos.com/v1
#       kind: ServiceMonitor
#       metadata:
#         name: pssst
```

```sh
kubectl apply -k .
```

Set `blackbox.base_url` in your inventory to `http://pssst-blackbox:9115`.

What you get:

| Object | Name | Details |
| --- | --- | --- |
| Deployment | `pssst` | 1 replica, `ghcr.io/adeleglise/pssst`, port 9099. Requests 50m CPU and 64Mi, limits 500m and 256Mi. Non-root (65532), read-only root filesystem, all capabilities dropped, no service account token. |
| Service | `pssst` | ClusterIP, port `metrics` 9099. |
| Deployment | `pssst-blackbox` | `quay.io/prometheus/blackbox-exporter:v0.28.0`, port 9115, same hardening. Not scraped: PSSST already exports what it measures. |
| Service | `pssst-blackbox` | ClusterIP 9115, reached only by PSSST. |
| ConfigMap | `pssst-config-<hash>` | Your inventory as `config.yml`. The hash rolls the pods when it changes. |
| ConfigMap | `pssst-blackbox-config-<hash>` | The Blackbox modules. |
| ServiceMonitor | `pssst` | For the Prometheus Operator: 30s interval, 10s timeout. |

The `pssst` pod also carries `prometheus.io/*` annotations for an annotation-
based Prometheus and the Datadog Autodiscovery check described below.
Liveness asks only whether the process serves HTTP; readiness and the startup
probe wait until every source and probe has been tried once, so a rollout
never sends scrapes to an empty cache.

Verify:

```sh
kubectl -n monitoring rollout status deploy/pssst
kubectl -n monitoring port-forward svc/pssst 9099 &
curl -s localhost:9099/readyz && curl -s localhost:9099/metrics | grep -c '^psp_'
kubectl -n monitoring exec deploy/pssst -- /pssst-check -config /etc/pssst/config.yml
```

## Deploy with Docker Compose

For a Prometheus already running in Docker,
[deploy/compose.yml](deploy/compose.yml) runs the exporter and its Blackbox on
Prometheus's network, with no published port. It builds both images from the
checkout with its inventory and modules baked in, so the natural workflow is a
fork: edit `deploy/pssst.psp.yml`, commit, redeploy. It also works as a GitOps
stack (a Portainer "Repository" stack, for one) that redeploys on each commit.

```sh
PROMETHEUS_NETWORK=monitoring PSSST_VERSION=1.1.0 docker compose -f deploy/compose.yml up -d
```

Prometheus then scrapes `pssst:9099`. To run the published image with a
mounted inventory instead:

```sh
docker network create pssst
docker run -d --name pssst-blackbox --network pssst \
  -v "$PWD/deploy/blackbox/blackbox.yml:/etc/blackbox/blackbox.yml:ro" \
  quay.io/prometheus/blackbox-exporter:v0.28.0 --config.file=/etc/blackbox/blackbox.yml
docker run -d --name pssst --network pssst -p 127.0.0.1:9099:9099 \
  -v "$PWD/inventory.yml:/etc/pssst/config.yml:ro" ghcr.io/adeleglise/pssst:latest
```

## Prometheus

**Scrape.** On Kubernetes, the ServiceMonitor or the pod annotations cover it;
make sure the ServiceMonitor's labels match your Prometheus's
`serviceMonitorSelector` (kube-prometheus-stack selects `release: <name>` by
default). Elsewhere, add the job in
[deploy/prometheus/pssst-scrape.yml](deploy/prometheus/pssst-scrape.yml):

```yaml
scrape_configs:
  - job_name: pssst
    scrape_interval: 30s
    static_configs:
      - targets: ["pssst:9099"]
```

**Rules and alerts.** [deploy/prometheus/pssst.yml](deploy/prometheus/pssst.yml)
holds seven recording rules and seven alerts. Add it to `rule_files`, or with
the Prometheus Operator wrap it in a PrometheusRule:

```sh
yq '{"apiVersion": "monitoring.coreos.com/v1", "kind": "PrometheusRule",
     "metadata": {"name": "pssst"}, "spec": .}' deploy/prometheus/pssst.yml \
  | kubectl -n monitoring apply -f -
```

| Rule | Value |
| --- | --- |
| `psp:observed_unavailable` | 1 when any fresh endpoint fails, 0 when all are fresh and up, absent when partly unknown. |
| `psp:declared_unavailable` | 1 or 0 while a configured source is fresh; absent for stale and `type: none`. |
| `psp:confirmed_incident` | Both signals failing, unless a maintenance window is open with no incident declared. |
| `psp:unannounced_failure` | Observed failing, declared fresh and quiet, no maintenance window. |
| `psp:status_source_stale` | No fresh complete snapshot. |
| `psp:probe_collection_unavailable` | Blackbox cannot be reached or answers badly. |
| `psp:probe_collection_fresh` | Every endpoint has a fresh collection. |

| Alert | `for` | Severity |
| --- | --- | --- |
| `PSPObservedUnavailable` | 5m | warning |
| `PSPConfirmedIncident` | 5m | critical |
| `PSPUnannouncedFailure` | 10m | critical |
| `PSPProbeCollectionUnavailable` | 5m | warning |
| `PSPStatusSourceUnavailable` | 10m | warning |
| `PSPCertificateExpiringSoon` (14 days) | 15m | warning |
| `PSPMaintenanceApproaching` (7 days) | 15m | info |

Freshness is the age of the last success, never the last attempt: one failed
poll keeps the last observation and does not restart an alert timer. `job` and
`instance` survive every aggregation, so two replicas never hide one another.
Route on the `severity` label in Alertmanager.

```promql
psp:unannounced_failure == 1
psp:confirmed_incident == 1
sum by (psp) (rate(psp_probe_collection_total{outcome="failure"}[5m]))
psp_info{kind="bank"}
```

## Datadog

Datadog bills custom metrics per series, so PSSST ships an **explicit, minimal
list**: about 300 series for the default inventory instead of about 1,070.

- **Kubernetes:** the `pssst` pod already carries the Autodiscovery annotation
  in [deployment.yaml](deploy/kubernetes/deployment.yaml). An agent with
  Autodiscovery picks it up with nothing to configure.
- **Host or Docker agent:** copy
  [deploy/datadog/openmetrics.yaml](deploy/datadog/openmetrics.yaml) to the
  agent's `conf.d/openmetrics.d/pssst.yaml`, set `openmetrics_endpoint`, and
  restart the agent.

```yaml
instances:
  - openmetrics_endpoint: http://pssst:9099/metrics
    namespace: pssst
    tag_by_endpoint: false
    metrics:
      - psp_exporter_build_info
      - psp_status_source_fresh
      - psp_declared_operational
      - psp_active_incidents
      - psp_maintenance_active
      - psp_probe_fresh
      - psp_probe_success
```

Metrics arrive as `pssst.psp_*`, tagged `psp`, `endpoint`, `component` and
`severity`.

| Shipped | Series | Why |
| --- | --- | --- |
| `psp_declared_operational` | 55 | Declared state, `overall` plus mapped components. |
| `psp_active_incidents` | 105 | Declared incidents per severity. |
| `psp_maintenance_active` | 21 | Planned work is not an incident. |
| `psp_status_source_fresh` | 21 | Whether the declared values are current. |
| `psp_probe_success` | 48 | Observed state. |
| `psp_probe_fresh` | 48 | Whether the observed value is current. |
| `psp_exporter_build_info` | 1 | Which version runs. |

Two settings matter. **The `*_fresh` gauges:** Datadog queries have no
`time()`, and the exporter keeps reporting a dead source's last good value, so
without them a silent source reads as healthy forever. Always multiply a state
by its freshness. **`tag_by_endpoint: false`:** by default the check tags every
series with its scrape URL, which carries the pod IP, so every reschedule would
create a new set of billed series.

Opt-in extras, with their cost for the default inventory:

| Add | Series | For |
| --- | --- | --- |
| `psp_probe_duration_seconds` | 48 | Latency dashboards. |
| `psp_maintenance_scheduled` | 21 | Showing announced windows. |
| `psp_status_poll`, `psp_probe_collection`, `psp_probe_result` | 234 | Error rates of the collection itself. List counters **without** `_total`: the check skips the suffixed name. |

Datadog runs none of the recording rules. These monitors reproduce them, as
multi-alerts on the grouping shown:

| Monitor | Queries | Formula, alert above 0 |
| --- | --- | --- |
| Observed failure, by `psp,endpoint` | `a = min:pssst.psp_probe_success{*}`, `b = min:pssst.psp_probe_fresh{*}` | `(1 - a) * b` |
| Declared problem, by `psp` | `a = min:pssst.psp_declared_operational{component:overall}`, `b = sum:pssst.psp_active_incidents{*}`, `c = min:pssst.psp_status_source_fresh{*}` | `clamp_max((1 - a) + b, 1) * c` |
| Unannounced failure, by `psp` | `a = min:pssst.psp_probe_success{*}`, `b = min:pssst.psp_probe_fresh{*}`, `c = min:pssst.psp_declared_operational{component:overall}`, `d = sum:pssst.psp_active_incidents{*}`, `e = sum:pssst.psp_maintenance_active{*}`, `f = min:pssst.psp_status_source_fresh{*}` | `(1 - a) * b * c * f * (1 - clamp_max(d + e, 1))` |

Use evaluation windows matching the Prometheus alerts: 5m, and 10m for
unannounced failure. A provider with `type: none` has no declared series, so
the unannounced formula has no data for it, which is correct: nobody was
listening.

## Grafana

[deploy/grafana/](deploy/grafana/) holds a generated, tabbed dashboard for
Grafana 12+: who is down and who is in maintenance first, then declared status,
probes and collection health. See that directory's README to import it.

## Metrics reference

Every metric has an explicit type. Labels come only from the configuration
(`psp`, `endpoint`, `component`) or from documented enums (`kind`, `severity`,
`outcome`, `version`). **Nothing remote ever becomes a label.**

| Metric | Type | Meaning |
| --- | --- | --- |
| `psp_exporter_build_info{version}` | gauge | Always 1. |
| `psp_info{psp,kind}` | gauge | Always 1. Join on `psp` to filter by kind. |
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

The counters separate two failures a gauge cannot: a rising
`probe_collection_total{outcome="failure"}` means **you** cannot measure; a
rising `probe_result_total{outcome="failure"}` means **the provider** is
failing.

`stale_after` is `3 * (interval + jitter + timeout)` per signal. A timestamp of
0 means "never", not "now". Per provider: 2 series, plus 16 and one per mapped
component when a declared source is configured, plus 12 to 14 per collected
probe; the default inventory exports about 1,070.

## How declared status is read

Every adapter returns **one complete snapshot or an error**: a partial document
fails the whole poll and shows up as a stale source, never as a half-truth.
Every adapter treats a **state it does not recognize as not operational**.
`overall` is false as soon as the page rollup or any published component says
so, and a mapped component missing from the page fails the snapshot.

| Adapter | Reads | Active incident | Maintenance |
| --- | --- | --- | --- |
| `statuspage_v2` | `/api/v2/summary.json` (Atlassian Statuspage) | Any incident not `resolved` or `postmortem`. Impact gives the severity. | `scheduled`, or active while `in_progress` or `verifying`. An undated window counts as scheduled. |
| `instatus_v1` | `/summary.json` and `/v2/components.json`, both required | Any incident not `RESOLVED`. The scale stops at `MAJOROUTAGE`, so `critical` never comes from here. | `NOTSTARTEDYET` or `INPROGRESS`. |
| `hipay_v1` | The monitor-list API behind HiPay's page; `base_url` is that full URL | None published: always zero. Only class `success` is operational. | None published: always zero. |
| `kener_v1` | A rendered [Kener](https://github.com/rajnandan1/kener) page, current-state node only | None published: always zero. Only state `up` is operational. | None published: always zero. |
| `adyen_v1` | `/api/incident-messages/active` | Every active message. No component inventory. | None published: always zero. |
| `paypal_v1` | `/api/v1/events`, also covers Braintree | Every event except `closed` or `sandbox` ones, and except open production maintenance. | An open production window, active once its start has passed. |
| `none` | Nothing | Declared status is **absent**, which is not healthy. | |

Severities are normalized to `none`, `minor`, `major`, `critical` and
`unknown`. An incident of severity `none` is still a declared incident. Remote
identifiers reach logs only when they already match a safe token shape.

## Default inventory

[deploy/pssst.psp.yml](deploy/pssst.psp.yml). Every URL and component ID was
resolved against the live page before being written down; run
`pssst-check -config` to see the current state.

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

Treezor and Powens serve their APIs on customer-specific hosts, so their
observed signal measures the public website, not the API. Corrections and new
providers are welcome as pull requests.

## Operations

**Endpoints.** `/metrics` only reads an in-memory cache: a scrape never waits
on a provider. `/healthz` is liveness. `/readyz` succeeds once every source and
probe has completed a first attempt, failed or not, and turns false during
shutdown. A restart empties the cache until the first polls.

**Logging.** JSON on stderr, with `service` and `version` on every record.
Level from `-log-level` or `PSSST_LOG_LEVEL` (`debug`, `info`, `warn`,
`error`). Logs carry error classes and safe incident IDs, never URLs, headers,
bodies or remote prose. A probe that Blackbox collected and reports failed is a
WARN: that is the provider failing, not PSSST.

**Security.** Responses are capped, redirects refused, URLs with credentials
rejected, secrets expanded from the environment. The image runs as non-root
on `scratch` with CA roots only. Restrict access to port 9099: it exposes your
provider inventory.

## Development

```sh
make fmt-check lint vuln test test-race build rules-test   # needs ripgrep and promtool
make smoke                                                 # real containers, real Prometheus
```

CI runs all of it on every pull request. Read [AGENTS.md](AGENTS.md) before
changing anything, human or agent: it holds the invariant, the rules that fail
the build, and the lessons already paid for. A new adapter is one entry in
`internal/source` plus its package under `internal/status`. Design decisions
are in [docs/adr](docs/adr). Releases are listed in
[CHANGELOG.md](CHANGELOG.md); a `vX.Y.Z` tag publishes the binaries and the
image.

## Known limits

- Configuration is static; a change needs a restart.
- No history ingestion and no provider-specific business-health decision.
- `kener_v1` reads markup, the weakest contract: an upstream redesign takes the
  source down, visible as a stale declared signal, never a wrong one.
- HiPay and Kener publish no incidents or maintenance, Adyen no maintenance;
  those series export 0.
- An open maintenance window mutes the unannounced signal for the whole
  provider, even when it covers an unrelated component.
- The Blackbox request carries no scrape-timeout header, so module timeouts
  must stay below `polling.timeout`.
- No NetworkPolicy ships with the Kubernetes manifests.

## License

[MIT](LICENSE).
