# AGENTS.md

Working notes for anyone, human or agent, changing this repository. The README
says what PSSST does; this says what will break if you are careless, and what
has already been paid for once. `CLAUDE.md`, `GEMINI.md` and `CODEX.md` only
point here: edit this file, never those.

## The invariant

**Declared status and observed status never merge inside the exporter.**

Everything else is negotiable. This is not. The value of the tool is the
disagreement between the two signals: an unannounced failure is a provider
failing while its own status page says nothing, and you can only see it if both
signals reached Prometheus untouched. Any change that folds them into one
"health" number, in Go or in a rule, destroys the product.

Four corollaries, each already encoded in tests:

1. **Absent is not healthy.** A provider with `type: none`, a source that never
   answered, a stale snapshot: all of these are *unknown*. Exporting them as
   operational is the worst bug this codebase can have, because it is silent.
2. **Unknown is not operational.** A component state an adapter does not
   recognize is reported as not operational. Never optimistically.
3. **A snapshot is atomic.** Half a document is an error, not a partial update.
   A failed poll updates health and freshness and leaves the last known good
   observation alone.
4. **A value travels with its freshness.** Last known good is only true while
   it is fresh. Every consumer must be able to tell: the rules compare the
   timestamps with `time()`, Datadog reads the `*_fresh` gauges.

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
- **The Datadog list is a cost decision, not a mirror of `/metrics`.** It lives
  in `deploy/kubernetes/deployment.yaml` and is explicit and minimal because
  custom metrics are billed per series. A new metric stays out unless a
  Datadog monitor needs it; when one goes in, add its series count to the
  README table. A counter goes in without its `_total` suffix: the OpenMetrics
  check silently skips the suffixed name. `TestDatadogListNamesRealMetrics`
  fails on a name the exporter does not define.

## Before claiming anything works

```sh
make fmt-check lint vuln test test-race build rules-test
make smoke        # real containers, real Prometheus, synthetic provider
```

`rules-test` needs `promtool` and `smoke` a container engine. Where either is
missing, say which check did not run rather than claiming the set passed; CI
runs both. `smoke` is the one that catches integration mistakes: it drives the declared
and observed signals independently and asserts the recording rules produce
`psp:unannounced_failure` and `psp:confirmed_incident`. If you changed rules,
adapters, the cache or the collector, run it.

Then look at real output before saying it is done:

```sh
bin/pssst-check -config deploy/pssst.psp.yml   # every declared source, one line each
bin/pssst-check -type <adapter> -url <url>
curl -s localhost:9099/metrics | grep '^psp_'
```

The inventory audit is how "the statuses are right" gets checked: open each
provider's page next to its line. A sandbox without egress to the status hosts
cannot do it; every line then reads `error`, never `operational`, and that is
the tool working.

Deployed changes get verified against the live Prometheus, not assumed:

```sh
curl -s -G --data-urlencode 'query=count(psp_status_source_up == 1)' \
  http://PROMETHEUS/api/v1/query
```

## Layout

| Path | Role |
| --- | --- |
| `internal/status/` | One package per adapter. `status.go` holds the interface, the severity enum and the rules every adapter shares: `ResolveComponents`, `AddIncident`, `SafeID`. |
| `internal/source/` | The one list of adapters. Config validation, the scheduler and `pssst-check` all read it. |
| `internal/blackbox/` | Blackbox client. Parses exposition, keeps only bounded scalars. |
| `internal/cache/` | The only mutable state. Race-safe, holds counters and freshness. |
| `internal/collector/` | Reads the cache. **Never does I/O.** |
| `internal/scheduler/` | One non-overlapping jittered loop per signal. |
| `internal/httpclient/` | Bounded GET: timeouts, size cap, no redirects, sanitized errors. |
| `cmd/pssst-check/` | Operator tool: resolve one source, print the snapshot. |
| `deploy/` | Inventory, rules, compose, Kubernetes, generated dashboard. |
| `.github/workflows/` | `ci.yml`: every check plus the smoke test. `release.yml`: a tag becomes a release. |
| `CHANGELOG.md` | One section per version; the release workflow publishes it as notes. |
| `docs/validation.md` | Evidence gathered for the last release. |
| `docs/specification.md` | The original brief, unmodified. |

## Repository and releases

`origin` is `github.com/adeleglise/pssst`, private and canonical; the module
path matches it. `gitea` is a mirror: push `main` and every tag there too.

Changes reach `main` through a pull request whose CI is green, `verify` and
`smoke` both, after a review by someone who did not write the change. CI takes
Go from `go.mod`; the Dockerfile pins its own `golang` builder image, so the
two move together. staticcheck and govulncheck are pinned in `go.mod` as tools.
Dependabot proposes updates weekly.

A release, in order:

1. Add a `## [X.Y.Z] - YYYY-MM-DD` section to `CHANGELOG.md` and record the
   evidence in `docs/validation.md`. Merge.
2. Tag `main`: `git tag -a vX.Y.Z -m vX.Y.Z`, then push the tag to `origin`
   and `gitea`.
3. The release workflow reruns `fmt-check lint vuln test test-race` and the
   rule tests, builds the binaries with `make dist` and publishes them with
   `SHA256SUMS`. It fails when the changelog has no section for the tag.

The exported version drops the `v`: tag `v1.0.0` reports `1.0.0` in
`psp_exporter_build_info`, like the untagged 0.x builds did. `make dist` is
reproducible: on a clean checkout of a tag, with the Go version in `go.mod`,
it gives the published checksums.

## Adding an adapter

Copy the shape of `internal/status/instatus`, which is the most complete one,
and build the snapshot with the helpers in `internal/status` rather than
rewriting them: `NewSnapshot`, `ResolveComponents`, `AddIncident`,
`AddScheduledMaintenance`.

1. Write the tests first, including: a quiet page, a degraded component, an
   unknown state, a missing mapped component, a malformed document, a timeout,
   and free text in an identifier field.
2. Implement `Fetch(ctx) (status.Snapshot, error)`. One complete snapshot or an
   error.
3. Add one entry to `internal/source`. Say whether the source publishes
   components, and whether their keys are displayed labels; validation,
   the scheduler and `pssst-check` follow from it.
4. Add a row to the adapter table in the README, saying what counts as an
   active incident and as maintenance.
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
Statuspage also moves a resolved incident to `postmortem`, which was counted
as an active incident of unknown state.

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

**A missing number is not zero.** HiPay's `psp.totalMonitors` was decoded into
a plain `int`, so an absent key read as 0, the pagination stopped after one
page and a partial list exported as healthy. A count the adapter relies on is
a pointer, and its absence fails the snapshot.

**An unrecognized remote value counts; skip only known ones.** The PayPal adapter kept
only `open` production events and skipped the rest, so an unknown state or an
empty environment silently left the provider operational. Skip only the
values known to be irrelevant (`closed`, `sandbox`); anything else counts.

**A failed poll is not a stale signal.** Rules gated on `*_up == 1` dropped a
signal whose last success was still fresh, which restarted the `for` timer of
every paging alert: a source failing one poll in ten could never page.
Correlation rules look only at the age of the last success against
`*_stale_after_seconds`; only the source-health rules look at the last attempt.

**A maintenance window is not an incident.** Statuspage and Instatus mark a
component under maintenance as not operational, so a failure inside an
announced window paged as a critical incident. An open window excludes the
provider from `psp:unannounced_failure`, and from `psp:confirmed_incident`
unless an incident is declared alongside it.

**Probe what the status page describes.** Adyen was probed on its test
environment against a status page that describes production. Live payment
APIs often sit behind a merchant-specific prefix, which is a customer
identifier and stays out of the inventory: probe a shared live host instead,
here `checkoutshopper-live.adyen.com`.

**Datadog has no `time()`.** Its metric queries cannot compare a timestamp
with now, and the exporter keeps reporting a dead source's last good value,
so the freshness test the rules apply was impossible there: a silent source
read as healthy forever. The `*_fresh` gauges carry that test, per signal.
They never combine declared and observed.

**The OpenMetrics check tags by scrape URL.** `tag_by_endpoint` defaults to
true, and the URL holds the pod IP: every reschedule minted a new set of
billed series, under an `endpoint` tag that collided with PSSST's own label.
It is off.

**An unused adapter is a loaded gun.** A `none` adapter returning an empty
successful snapshot sat in the tree, unused. Wired by mistake, it would have
exported an absent source as freshly polled. It is gone, and the registry test
asserts `none` has no adapter.

**`rg PATTERN` without a path searches stdin.** When stdin is not a terminal,
search mode reads it and waits: a scripted `rg -l` hung this way. Always pass
a path. `rg --files` does not read stdin. The `ubuntu-latest` runner does not
ship ripgrep, so the workflows install it.

**The committed stack drifted from the deployed one.** `compose.nas.yml`
described config mounts, a published port and no shared network, while the
NAS ran images carrying their config on `monitoring_default`. Diff the live
stack file against the repository before every deployment.

## Operating on the real infrastructure

Production is read-only until explicitly authorized for that exact action.
Before editing a live Prometheus configuration: back it up, validate the
candidate with `promtool check config` from the directory holding the rule
files so relative paths resolve, then reload. Verify the target is `up` and the
rules are `ok` afterwards, through the API, not by assumption.

Never publish a port that does not need publishing. The exporter joins the
network its Prometheus already runs on and is reached by service name.

### The NAS

```text
Portainer  https://192.168.1.250:19943   endpoint 2, stack 56 "pssst"
Prometheus http://192.168.1.250:9090     3.11.3, stack "monitoring", reload enabled
Rules      nas-signoz/prometheus/pssst.rules.yml, relative in rule_files,
           reachable over NFS at /Volumes/repos-nas/misc/projects/nas-signoz
Token      macOS keychain, service portainer-api-token; never in a file
```

No registry serves the images: they are built on a workstation and loaded.
Deploying version X.Y.Z, from a clean checkout of its tag:

1. Build both images for the NAS architecture. The Dockerfile cross-compiles,
   so nothing is emulated:
   `podman build --platform linux/amd64 --target exporter-nas --build-arg VERSION=X.Y.Z -t localhost/pssst-nas:X.Y.Z .`
   and the same with `--target blackbox-nas -t localhost/pssst-blackbox-nas:X.Y.Z`.
2. Check the result: `podman run --rm localhost/pssst-nas:X.Y.Z -version`
   prints `X.Y.Z`, and `/etc/pssst/config.yml` copied out of the image equals
   the tag's `deploy/pssst.psp.yml`.
3. Load both into the NAS engine: `podman save --format docker-archive -m`
   into a tar, then `POST /api/endpoints/2/docker/images/load` on Portainer.
   The image IDs on the NAS must match the local ones.
4. Save the live stack file (`GET /api/stacks/56/file`), diff it against
   `deploy/compose.nas.yml`, then `PUT /api/stacks/56?endpointId=2` with that
   file and `pullImage: false`, since the images are local.
5. For rules: copy the Prometheus directory aside, drop the candidate in as
   `pssst.rules.yml`, and run `promtool check config` there with the promtool
   of the production version. Then back up the live file as
   `pssst.rules.yml.bak.<epoch>`, replace it and `POST /-/reload`.
6. Verify through the API: `psp_exporter_build_info` reports `X.Y.Z`, every
   configured source and probe is up, all pssst rules are `ok`. A reload keeps
   alert state, but a redeployed exporter starts with an empty cache, so every
   `for` timer restarts: maintenance alerts reappear after fifteen minutes.

Rollback: the previous images stay on the NAS; restore the saved stack file
and the rules backup, then reload.
