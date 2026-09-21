# Validation, v1.0.0, 2026-09-21

Evidence gathered before tagging v1.0.0. The MVP validation of 2026-09-18 is
in the git history of this file.

## Production before the release

The NAS runs 0.5.0. Its Go code is the release base, 724c10b: no Go file
changed after b584cf0. Inspected read-only through the NAS Prometheus API and
the Portainer API; nothing on the NAS was modified.

| Check | Result |
| --- | --- |
| Scrape target `pssst:9099` | `up`, 3.7 ms scrape, no error; `avg_over_time(up[7d])` is 1. |
| `psp_exporter_build_info` | `version="0.5.0"`. |
| Declared sources | 21 configured, 21 with `psp_status_source_up == 1`. |
| Providers | 24 `psp_info` series. |
| Probes | 48 collected, 48 successful. |
| Failures over the last hour | No failed status poll, no failed collection. |
| Rules | 13 loaded from `/etc/prometheus/pssst.rules.yml`, all `ok`, expressions identical to `deploy/prometheus/pssst.yml` at 724c10b. |
| Firing | `PSPMaintenanceApproaching` (info) for six providers with announced windows. |
| Stack | Portainer stack `pssst`: `localhost/pssst-nas:0.5.0` and `localhost/pssst-blackbox-nas:0.4.0`, both on `monitoring_default`, no published port. |

The stack differed from `deploy/compose.nas.yml`, which could not have been
scraped as committed. The file now matches the running stack.

## Release branch

| Check | Result |
| --- | --- |
| `make fmt-check lint vuln test test-race build rules-test` | Pass. staticcheck clean, govulncheck reports no vulnerability, 14 rules, all promtool fixtures. |
| `make smoke` | Pass on Podman 6.1.1: both signals driven independently, `psp:unannounced_failure` and `psp:confirmed_incident` reached, recovery observed. |
| Rule suite repeated 10 times | 10 passes: no evaluation-order flakiness. |
| `make dist` run twice | Identical `SHA256SUMS`. |
| `go mod verify` | All modules verified. |
| `pssst-check` on the hardened adapters | PayPal and HiPay return the same snapshot as production: PayPal operational with two scheduled windows, HiPay operational. |

## Review

An independent Claude Opus review of the whole code at 724c10b returned **do
not ship**: two P0 and five P1 findings. All seven are fixed on the release
branch, each bug reproduced by a failing test before its fix.

| Finding | Fix |
| --- | --- |
| P0 PayPal dropped events with an unknown state or environment | Allow-list: only `closed` and `sandbox` are skipped. |
| P0 HiPay accepted a partial list when the total was missing | Total required, constant and met exactly. |
| P1 One failed poll reset the paging alerts | Correlation gated on freshness only. |
| P1 Announced maintenance paged as unannounced failure | Excluded from `psp:unannounced_failure`, and from `psp:confirmed_incident` unless an incident is declared; the warning still fires. |
| P1 NAS compose stack could not be scraped | Matches the running stack. |
| P1 Kubernetes image pinned to `pssst:0.5.0` | kustomize `images` entry, tag 1.0.0. |
| P1 Datadog skipped the three counters | Listed without `_total`, as the OpenMetrics check requires. |

The same reviewer then read the release branch and returned **ship with
fixes**: a failure inside a window that Statuspage or Instatus flags on its
components still raised `PSPConfirmedIncident`. A failing fixture reproduced
it before the fix. The other remarks were wording in the README, the
changelog and the release workflow, now corrected; the release workflow also
runs the rule tests.

The Adyen probes targeted the test environment, which the production status
page does not describe. They now target `checkoutshopper-live.adyen.com`, the
live host shared by every merchant; the live payment API needs a
merchant-specific prefix that does not belong in the inventory. Both modules
succeed against it through a local Blackbox 0.28.0: HTTP 200 and TLS.

## Deferred

- The Blackbox request carries no scrape-timeout header, so a module timeout
  at or above `polling.timeout` reads as a collection failure. The real
  inventory keeps its modules below it.
- Datadog reads last-known-good values without the freshness gating the
  rules apply; monitors there must copy it.
- Kener and HiPay publish no incidents and Adyen no maintenance, yet those
  series export 0.
- No NetworkPolicy ships with the Kubernetes manifests.
- An open window mutes the unannounced and confirmed signals for the whole
  provider, even when it covers an unrelated component.

## Deployment

Deployed on the NAS on 2026-09-21 with the procedure in `AGENTS.md`, then
verified through the Prometheus and Portainer APIs.

| Check | Result |
| --- | --- |
| Release binaries | Six binaries and `SHA256SUMS` published; a local `make dist` of the tag gives the same six hashes. |
| Images | Built from the tag for linux/amd64; the exporter reports `1.0.0` and carries the tag's inventory. The Blackbox image is byte-identical to 0.4.0, its configuration unchanged. |
| Rules | Full Prometheus configuration and rule tests validated with promtool 3.11.3, the production version, before the swap. |
| Stack | Both containers on `localhost/*:1.0.0`, `monitoring_default`, no published port. |
| `psp_exporter_build_info` | `version="1.0.0"`, target `up`. |
| Signals | 21 of 21 declared sources up, 48 of 48 probes successful, Adyen `checkout_web_https` answering 200. |
| Rules after reload | 14 loaded, all `ok`; no unannounced failure, no confirmed incident. |

Rollback material: the 0.5.0 images stay on the NAS, the previous stack file
is saved outside the repository, and the previous rules are in
`pssst.rules.yml.bak.1790016984` next to the live file.
