# AGENTS.md

Working notes for anyone, human or agent, changing this repository. The README
says what PSSST does; this says what will break if you are careless, and what
has already been paid for once.

## The invariant

**Declared status and observed status never merge inside the exporter.**

Everything else is negotiable. This is not. The value of the tool is the
disagreement between the two signals: an unannounced failure is a provider
failing while its own status page says nothing, and you can only see it if both
signals reached Prometheus untouched. Any change that folds them into one
"health" number, in Go or in a rule, destroys the product.

Three corollaries, each already encoded in tests:

1. **Absent is not healthy.** A provider with `type: none`, a source that never
   answered, a stale snapshot: all of these are *unknown*. Exporting them as
   operational is the worst bug this codebase can have, because it is silent.
2. **Unknown is not operational.** A component state an adapter does not
   recognize is reported as not operational. Never optimistically.
3. **A snapshot is atomic.** Half a document is an error, not a partial update.
   A failed poll updates health and freshness and leaves the last known good
   observation alone.

## Non-negotiable rules

- **Nothing remote becomes a label.** Not an incident title, a URL, an error
  message or a timestamp. Labels come from the config or from a documented
  enum. `internal/collector/collector_test.go` fails the build on any unknown
  label name; when you add a legitimate one, add it there and justify it.
- **Remote text never reaches a log field** unless it already matches a safe
  token shape. Adapters count an incident they cannot safely identify rather
  than logging its identifier.
- **Fail loudly, never guess.** A stale declared signal is a useful signal. A
  wrong declared signal feeds the correlation rules and manufactures false
  incidents.
- **Value types are explicit.** `prometheus.ValueType`'s zero value is invalid,
  so every metric definition states `gauge` or `counter`. A counter read as a
  gauge silently loses every rate.
- **Tests never touch a live provider.** Fixtures and `httptest` only. Use
  `.test` or `.invalid` hostnames. `bin/pssst-check` is the tool for looking at
  something real.
- **Adding a metric means adding it to the Datadog list** in
  `deploy/kubernetes/deployment.yaml`. That list is explicit because custom
  metrics are billed per series.

## Before claiming anything works

```sh
make fmt-check lint test test-race build rules-test
make smoke        # real containers, real Prometheus, synthetic provider
```

`smoke` is the one that catches integration mistakes: it drives the declared
and observed signals independently and asserts the recording rules produce
`psp:unannounced_failure` and `psp:confirmed_incident`. If you changed rules,
adapters, the cache or the collector, run it.

Then look at real output before saying it is done:

```sh
bin/pssst-check -type <adapter> -url <url>
curl -s localhost:9099/metrics | grep '^psp_'
```

Deployed changes get verified against the live Prometheus, not assumed:

```sh
curl -s -G --data-urlencode 'query=count(psp_status_source_up == 1)' \
  http://PROMETHEUS/api/v1/query
```

## Layout

| Path | Role |
| --- | --- |
| `internal/status/` | One package per adapter. `status.go` holds the interface and the severity enum. |
| `internal/blackbox/` | Blackbox client. Parses exposition, keeps only bounded scalars. |
| `internal/cache/` | The only mutable state. Race-safe, holds counters and freshness. |
| `internal/collector/` | Reads the cache. **Never does I/O.** |
| `internal/scheduler/` | One non-overlapping jittered loop per signal. |
| `internal/httpclient/` | Bounded GET: timeouts, size cap, no redirects, sanitized errors. |
| `cmd/pssst-check/` | Operator tool: resolve one source, print the snapshot. |
| `deploy/` | Inventory, rules, compose, Kubernetes, generated dashboard. |
| `docs/specification.md` | The original brief, unmodified. |

## Adding an adapter

Copy the shape of `internal/status/instatus`, which is the most complete one.

1. Write the tests first, including: a quiet page, a degraded component, an
   unknown state, a missing mapped component, a malformed document, a timeout,
   and free text in an identifier field.
2. Implement `Fetch(ctx) (status.Snapshot, error)`. One complete snapshot or an
   error.
3. Wire it in three places, all of which the compiler will not remind you
   about: `internal/config` validation, `internal/scheduler.New`, and
   `cmd/pssst-check`.
4. Add a row to the adapter table in the README.
5. Validate against the live source with `pssst-check` before adding it to the
   inventory.

If a source publishes no component inventory, leave components rejected in
validation rather than silently ignored, and leave maintenance counts at zero
rather than inferring them.

## Paid-for lessons

Each of these cost a full cycle. They are not hypothetical.

**Real pages are not the documented schema.** Statuspage omits `incidents`
entirely when empty (observed on SumUp), and publishes scheduled maintenance
with `scheduled_for: null` (SumUp, GoCardless). Both used to fail the whole
snapshot, making those providers permanently unusable. Required keys are
`components` and the page indicator; an absent list means nothing is active.

**A page that answers is not the right page.** `status.bridgeapp.com` is a
learning platform, and `bridge.instatus.com` is the default Instatus template.
Always check the component names against the company you expect.

**Look past the vanity domain.** Stripe, Fintecture, Treezor and Powens all
publish a Statuspage that the obvious `status.<company>.com` lookup misses.
Try `<name>.statuspage.io`, and follow redirects manually since the client
refuses them: Alma is `status.almapay.com`, GoCardless is
`www.gocardless-status.com`.

**A client-rendered page still has an API.** HiPay's HTML contains only
"Loading...", but its own script names the public monitor-list endpoint. Read
the scripts before concluding a provider publishes nothing.

**Same colour classes, different meaning.** Kener renders ninety daily history
bars using the same `bg-api-*` classes as the current state. A parser matching
on proximity reads history as the current status. Only `text-api-*` carries
current state, and `data-ts` nodes are history.

**Blackbox TCP probes prefer IPv6.** The HTTP modules set
`preferred_ip_protocol: ip4`, the TCP ones did not. On a host without routable
IPv6 that fails for exactly the targets publishing an AAAA record, and passes
everywhere else, so a workstation with working IPv6 hides it completely. Keep
both families on the same rule.

**Grafana 13 fails silently three ways.** A `GridLayout` nested inside a tab is
accepted, stored, and renders nothing: use `RowsLayout` with an
`AutoGridLayout` per row. An invented plugin version leaves panels stuck on
"Loading plugin panel"; the version field must be empty. Transformation options
placed directly under `spec` are kept but ignored; they belong under
`spec.options` alongside `id`.

**Grafana's v2 API creates orphans.** A dashboard created anonymously through
`/apis/dashboard.grafana.app/` cannot then be read or deleted by that same
anonymous caller. Create through `/api/dashboards/db` first, then update the
layout through the v2 API.

**kustomize refuses files outside its directory.** The kustomization lives in
`deploy/`, not `deploy/kubernetes/`, so the inventory can be generated into a
ConfigMap instead of duplicated.

## Operating on the real infrastructure

Production is read-only until explicitly authorized for that exact action.
Before editing a live Prometheus configuration: back it up, validate the
candidate with `promtool check config` from the directory holding the rule
files so relative paths resolve, then reload. Verify the target is `up` and the
rules are `ok` afterwards, through the API, not by assumption.

Never publish a port that does not need publishing. The exporter joins the
network its Prometheus already runs on and is reached by service name.
