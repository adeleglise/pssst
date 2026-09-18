# PSSST MVP design and plan review

The input specification is [the supplied plan](../../specification.md). The repository had no files, commits, CI, or conventions. Implement in Go on `feat/pssst-mvp`, in the existing empty checkout.

## Review decisions

The proposed separation of declared and observed signals is sound. Use independent bounded polling loops and immutable cache snapshots; scrapes never perform upstream I/O. A single shared scheduler that serializes all PSPs would let one slow provider delay the others; spawning work per tick would risk overlap. Use one context-aware loop per configured source/probe, with no overlapping polls and a configuration cap of 128 PSPs and 32 probes per PSP.

1. Use `/api/v2/summary.json`: this documented current-state endpoint includes components, unresolved incidents, upcoming and active maintenance. It is not a historical paginated listing. Reject incomplete snapshots atomically, preserving the previous successful snapshot.
2. Component `overall` is always exported. Optional `status.components` maps stable local component IDs to upstream IDs; only these mapped components get extra series. Remote component ID churn must not grow the label space. Unknown component states mean non-operational, never green. Missing configured components fail the snapshot.
3. A valid Blackbox response with `probe_success 0` is an observed failure with collection up. Transport, non-2xx, oversize, or invalid metrics mean collection down; preserve the previous observation. Add collection-up and last-collection-success metrics.
4. Source freshness is measured from the last successful retrieval, not Statuspage's content-change timestamp. Export per-source/probe stale thresholds of `3 * (interval + jitter + timeout)`. A source with no successful fetch is unknown. `type: none` is unconfigured, not broken or declared healthy.
5. Add `psp_maintenance_next_start_timestamp_seconds` (0 when no upcoming maintenance) so an approaching-maintenance alert is possible.
6. Readiness means each configured worker has completed its initial attempt, regardless of upstream outcome; one unavailable PSP must not remove the exporter's monitoring endpoint. During shutdown readiness is false.
7. Expand `${NAME}` in parsed scalar values, rejecting unset variables and avoiding YAML injection. Secrets go in configurable HTTP headers for Statuspage/Blackbox; probe auth lives in Blackbox modules. Never log raw config, URLs, transport errors, incident text, or response bodies. Logs identify PSP, endpoint, normalized incident ID/state/severity, and bounded error class only.
8. Do not retry individual polls in the MVP: the independent interval is the bounded retry mechanism and avoids multiplying load/outage latency. Jitter applies on startup and between polls.
9. Response bodies are limited to 2 MiB; reject redirects to prevent credential forwarding. Bound remote entity counts. HTTP clients have dial/TLS/header/idle/request timeouts.

## Files and interfaces

- `internal/config`: strict YAML parsing, defaults, field/URL/ID/duration checks, limits, env expansion.
- `internal/status`: `StatusProvider.Fetch(context.Context) (Snapshot, error)`; `statuspage` and `none` implementations.
- `internal/blackbox`: `ProbeProvider.Probe(context.Context, module, target string) (Result, error)` and allowlisted Prometheus decoding.
- `internal/httpclient`: bounded, sanitized HTTP helper shared by both adapters.
- `internal/cache`: configured inventory, mutex, snapshot copies, timestamps and initial-attempt readiness.
- `internal/scheduler`: independent polling, cache updates and bounded structured logs.
- `internal/collector`: custom Prometheus collector, cached gauges only.
- `internal/server`, `cmd/psp-exporter`: endpoints, signal cancellation, HTTP shutdown and flags limited to config path/version.
- `cmd/fake-psp`, `compose.yaml`, `deploy/`, `examples/`: isolated controllable demo, pinned image versions, rules and rule tests.
- `README.md`, `docs/adr/`: operations, metric/config contracts and delegation rationale.

## Validation

Use table-driven unit tests and local HTTP test servers for malformed/partial payloads, timeouts, body limits, probe versus collector failures, URL encoding, config secrecy, concurrency and cancellation. Integration tests run the real adapters/scheduler/cache/collector/server together and change both signals independently. Validate PromQL with promtool rule fixtures. Run format, vet, all tests, race, build, then Compose smoke tests including successful Prometheus scrapes and signal transitions.

## Scope limits

In-memory state resets on restart. Static config requires restart. No historical incident ingestion, generic scraping, authentication UI, arbitrary metric forwarding, hot reload or health decisions in Go. TLS/DNS/TCP behavior belongs to named Blackbox modules. Public demo endpoints are synthetic; examples never assume real PSP health URLs exist.

## Infrastructure updates

Use the running local Podman machine for builds and the synthetic Compose stack. Prepare reusable scrape/rule configuration and an importable Grafana dashboard for the existing Prometheus/Grafana on NAS `192.168.1.250`. A read-only Portainer lookup on port `19443` was refused at the TCP layer; no NAS deployment changes were made. The token is read from the macOS Keychain only and never stored in the repository.

## Protocol references

- [Statuspage public API example and current summary contract](https://www.githubstatus.com/api)
- [Prometheus Blackbox Exporter](https://github.com/prometheus/blackbox_exporter) and its [module configuration](https://github.com/prometheus/blackbox_exporter/blob/master/CONFIGURATION.md)
- [Grafana dashboard JSON models](https://grafana.com/docs/grafana/latest/visualizations/dashboards/build-dashboards/view-dashboard-json-model/) — the supplied dashboard uses the portable classic JSON model.
