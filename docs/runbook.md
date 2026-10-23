# Runbook

One section per alert in
[deploy/prometheus/pssst.yml](../deploy/prometheus/pssst.yml); each alert's
`runbook_url` points at its section. Every alert carries `psp`, `job` and
`instance`, except `PSPExporterAbsent`, which has no exporter to name.

PSSST reads two signals and never merges them. **Declared** is what the
provider's status page says. **Observed** is what Blackbox measured against
its endpoints. Each alert below reads one of them, or compares the two; the
first thing to establish is which, because the checks differ.

## First checks

These come up in every section.

- **`pssst-check`** reads the provider's declared source once, now, and prints
  what the exporter would export. Inside the cluster:
  `kubectl -n monitoring exec deploy/pssst -- /pssst-check -config /etc/pssst/config.yml`.
  For one source: `pssst-check -type <type> -url <base_url>`, both from the
  provider's entry in the inventory.
- **The provider's status page**, opened by hand. It may say more than the
  adapter reads, or have moved.
- **Your own error rate** on that provider: payment failures, timeouts and
  declines in your application's metrics. PSSST probes a shared host; it does
  not see your traffic, and your traffic is what matters.
- **The metrics**, per signal, never combined:
  `psp_probe_success{psp="<psp>"}` and `psp_probe_fresh` for observed,
  `psp_declared_operational{psp="<psp>"}`, `psp_active_incidents` and
  `psp_status_source_fresh` for declared.

## The observed start time

Write down when the observed failure began. It is the start of your own impact
window, the reference for reconciling payments and retries, and the evidence
when you compare it with the time the provider later declares, or with the
start its postmortem gives. The gap between the two is how long the provider
was failing without saying so.

Alertmanager's `startsAt` is when the alert fired, which is after its `for`.
The condition itself began earlier: `ALERTS_FOR_STATE` holds that time in Unix
seconds, and the recording rule's history shows it directly.

```promql
ALERTS_FOR_STATE{alertname="PSPUnannouncedFailure", psp="<psp>"}
psp:observed_unavailable{psp="<psp>"}        # graph it: the first 1
```

## PSPObservedUnavailable

**Means.** At least one probed endpoint of the provider has failed for five
minutes, on a fresh observation. Observed only: it says nothing about what the
provider declares. It also fires during an announced maintenance window, by
design, as a warning.

**Signal.** `psp:observed_unavailable`, then `psp_probe_success` per
`endpoint` to see which one, and `psp_probe_http_status_code` for HTTP probes.

**First checks.** Your own error rate on that provider: if it is flat, the
probed host may not be the one you use. The provider's page and `pssst-check`,
to see whether an incident or a maintenance window is open. If
`PSPUnannouncedFailure` or `PSPConfirmedIncident` follows, work from that one.

**Observed start.** Note it now. If the provider declares an incident later,
this is when it actually began.

## PSPObservedFlapping

**Means.** Over ten minutes, at least one probed endpoint failed more than 30%
of its probes, and this held for ten minutes. A probe that fails every other
minute never stays down five minutes in a row, so `PSPObservedUnavailable`
never fires for it; this does. A steady outage does not trigger it, but a
single outage of six to ten minutes does, after `PSPObservedUnavailable`.
Observed only.

**Signal.** `psp:probe_success_ratio_10m`, the share of probes that succeeded
on the worst fresh endpoint, then `psp_probe_success` per `endpoint`.

**First checks.** Your own error rate on that provider, which degrades the same
way when the failure is real. `psp_probe_duration_seconds` near the probe
timeout points at latency rather than errors. Check that Blackbox itself is
stable: `psp_probe_collection_total{outcome="failure"}` rising means you
cannot measure, not that the provider fails.

**Observed start.** The ratio's first drop below 0.7. An intermittent failure
is the kind providers declare last, when at all.

## PSPUnannouncedFailure

**Means.** The provider is failing, observed, for ten minutes, while its own
status page is fresh and reports no incident and no maintenance window. This
is the case PSSST exists for: the provider has not noticed, or has not said.
It compares the two signals; neither changes the other.

**Signal.** `psp:observed_unavailable` for the observed side,
`psp:declared_unavailable == 0` and `psp_status_source_fresh` for the declared
side.

**First checks.** Your own error rate on that provider first: it decides
whether this is your incident too. Then the provider's page by hand and
`pssst-check`, in case an incident appeared since the last poll or the page
says something the adapter does not read. Then contact the provider's support
with the observed start time, and consider failing over.

**Observed start.** It is the evidence of how long the provider failed before
declaring anything. Keep it with the time they eventually declare.

## PSPConfirmedIncident

**Means.** Both signals agree: the provider is failing, observed, and its fresh
status page declares an incident or a non-operational state. The provider
knows. An open maintenance window with no incident declared does not count.

**Signal.** `psp:confirmed_incident`; `psp_active_incidents` by `severity` and
`psp_declared_operational` by `component` for what is declared.

**First checks.** Your own error rate, to size the impact. The provider's page
for the declared scope and the updates. Whether the declared components are
the ones you use: `psp_declared_operational` by `component`.

**Observed start.** Compare it with the incident's start on the provider's
page. Probes often see the failure before the page says it.

## PSPStatusSourceUnavailable

**Means.** For ten minutes, PSSST has had no fresh complete snapshot of the
provider's status page: polls fail, or the page no longer parses. Declared
only. The declared signal is now **unknown**, not healthy, so
`PSPUnannouncedFailure` and `PSPConfirmedIncident` cannot fire for this
provider until it returns.

**Signal.** `psp:status_source_stale`, `psp_status_source_up`,
`psp_status_poll_total{outcome="failure"}`, and the exporter's logs, which
carry the error class.

**First checks.** `pssst-check` on that source: it prints the error. Then the
page by hand: it may have moved, changed schema, or started redirecting, which
the client refuses. A provider's page going down during an outage is itself a
hint: look at `PSPObservedUnavailable` for the same provider.

**Observed start.** Not applicable: this is about the declared source.

## PSPProbeCollectionUnavailable

**Means.** For five minutes, PSSST could not get a valid answer from Blackbox
for that provider's probes, or the last valid answer is stale. This is about
**your** measurement, not the provider. The observed signal is **unknown**.

**Signal.** `psp:probe_collection_unavailable`, `psp_probe_collection_up`,
`psp_probe_collection_total{outcome="failure"}`.

**First checks.** Blackbox itself: is it running, reachable from PSSST, and
does it know the module named in the inventory? If every provider alerts at
once, it is Blackbox or the network between them.

**Observed start.** Not applicable: nothing was observed.

## PSPCertificateExpiringSoon

**Means.** A certificate observed on one of the provider's endpoints expires
within 14 days. Observed only.

**Signal.** `psp_probe_ssl_earliest_cert_expiry_timestamp_seconds` per
`endpoint`.

**First checks.** Open the endpoint and read its certificate. A provider
usually rotates well before expiry; if it has not, tell them, and check
whether you pin that certificate anywhere.

**Observed start.** Not applicable.

## PSPMaintenanceApproaching

**Means.** The provider's fresh status page announces maintenance starting
within seven days. Declared only. Informational: route it to a channel, not a
pager.

**Signal.** `psp_maintenance_next_start_timestamp_seconds`,
`psp_maintenance_scheduled`.

**First checks.** The provider's page for the window and the affected scope.
During the window, a failure raises `PSPObservedUnavailable` but neither
`PSPUnannouncedFailure` nor `PSPConfirmedIncident`.

**Observed start.** Not applicable.

## PSPExporterAbsent

**Means.** No Prometheus target has exposed `psp_exporter_build_info` for ten
minutes: the exporter is down, or not scraped. Every PSSST series is gone, so
every other alert is silent. Absent is not healthy; this alert is what turns
that silence into a page.

It fires only when no PSSST exporter is scraped at all. A single failed
replica among several needs an `up == 0` alert on your own scrape job's name,
which PSSST cannot know.

**Signal.** `up` for your PSSST scrape job, and `psp_exporter_build_info`.

**First checks.** The pod or container: `kubectl -n monitoring rollout status
deploy/pssst` and its logs. Then the scrape: Prometheus's targets page. On the
Prometheus Operator, a ServiceMonitor whose labels do not match the
`serviceMonitorSelector` is never scraped. A rule file loaded without its
scrape job gives exactly this alert.

**Observed start.** Not applicable: during the gap, PSSST measured nothing,
and nothing about providers can be concluded from it.
