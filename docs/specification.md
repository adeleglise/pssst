# PSSST


You are starting development of a production-grade Prometheus exporter called `pssst` Payment Status Signals & Surveillance Tool.

## Objective

Build a unified monitoring service for payment service providers (PSPs). It must expose normalized Prometheus metrics combining two independent signals:

1. **Declared status**
   - Official PSP status-page incidents
   - Component status
   - Scheduled maintenance
   - Freshness and availability of the official status source

2. **Observed status**
   - HTTP, TCP, DNS, and TLS probes
   - Lightweight API checks
   - Probe latency, HTTP status, and certificate expiry
   - Use an existing Prometheus Blackbox Exporter instance rather than reimplementing its probe engine

The service must preserve declared and observed status as separate signals. Correlation and effective-health decisions should happen through Prometheus recording and alerting rules.

## First step: inspect and plan

Before editing:

1. Inspect the repository structure, conventions, existing build system, CI, deployment files, and documentation.
2. Identify anything reusable.
3. Summarize the proposed architecture and file changes.
4. Then implement the MVP without waiting for confirmation unless an important requirement is genuinely ambiguous or unsafe.

Adapt to existing repository conventions. If this is an empty repository, use Go and the structure proposed below.

## MVP scope

Implement a vertical slice supporting:

- Atlassian Statuspage API v2
- PSPs without a public status page (`type: none`)
- HTTP/TCP/TLS probing delegated to Blackbox Exporter
- Background polling with an in-memory, concurrency-safe cache
- One `/metrics` endpoint exposing normalized PSP metrics
- `/healthz` and `/readyz`
- YAML configuration with strict validation
- Graceful shutdown
- Structured logs
- Unit and integration tests
- Docker image and local Docker Compose environment
- Example Prometheus scrape configuration
- Recording and alerting rules
- README with setup and operating instructions

Design adapter interfaces so custom HTML, RSS, webhook, and manual-status adapters can be added later, but do not implement a generic HTML scraper in the first iteration.

## Suggested architecture

Use Go unless the repository already establishes a better-supported implementation language.

Suggested packages:

```text
cmd/psp-exporter/
internal/config/
internal/collector/
internal/status/
internal/status/statuspage/
internal/status/none/
internal/blackbox/
internal/cache/
internal/server/
deploy/prometheus/
examples/
```

Main components:

- `StatusProvider` interface for official status sources
- `ProbeProvider` interface for observed checks
- Statuspage API v2 implementation
- Blackbox Exporter HTTP client implementation
- Scheduler that polls providers independently using configurable intervals, timeouts, and jitter
- Thread-safe state cache
- Prometheus collector that reads only from the cache
- HTTP server exposing metrics and health endpoints

Do not perform external PSP requests synchronously inside a Prometheus scrape. `/metrics` must be fast and serve the latest cached state.

## Configuration

Support a configuration similar to:

```yaml
server:
  listen_address: ":9099"

polling:
  status_interval: 60s
  probe_interval: 30s
  timeout: 10s
  jitter: 5s

blackbox:
  base_url: "http://blackbox-exporter:9115"

psps:
  - id: payplug
    display_name: Payplug
    status:
      type: statuspage_v2
      base_url: "https://status.payplug.com"
    probes:
      - id: payment_api
        module: http_2xx
        target: "https://api.payplug.com/health"

  - id: fintecture
    display_name: Fintecture
    status:
      type: none
    probes:
      - id: payment_api
        module: http_2xx
        target: "https://api.fintecture.com/health"
```

Requirements:

- Reject unknown YAML fields.
- Validate unique PSP and probe IDs.
- Validate URLs, durations, required fields, and supported adapter types.
- Permit environment-variable references for secrets.
- Never expose credentials in metric labels or logs.
- Avoid putting sensitive values in command-line arguments.
- Clearly distinguish missing configuration from a failed status source.

## Metrics

Use stable, low-cardinality labels. At minimum expose:

```prometheus
psp_exporter_build_info{version="..."} 1

psp_status_source_configured{psp="payplug"} 1
psp_status_source_up{psp="payplug"} 1
psp_status_source_last_success_timestamp_seconds{psp="payplug"} 1789720800
psp_status_source_last_poll_timestamp_seconds{psp="payplug"} 1789720800

psp_declared_operational{psp="payplug",component="payment_api"} 0
psp_active_incidents{psp="payplug",severity="major"} 1
psp_maintenance_active{psp="payplug"} 0
psp_maintenance_scheduled{psp="payplug"} 1

psp_probe_success{psp="payplug",endpoint="payment_api"} 0
psp_probe_duration_seconds{psp="payplug",endpoint="payment_api"} 2.41
psp_probe_http_status_code{psp="payplug",endpoint="payment_api"} 503
psp_probe_ssl_earliest_cert_expiry_timestamp_seconds{psp="payplug",endpoint="payment_api"} 1795000000
psp_probe_last_poll_timestamp_seconds{psp="payplug",endpoint="payment_api"} 1789720830
psp_probe_last_success_timestamp_seconds{psp="payplug",endpoint="payment_api"} 1789720700
```

You may adjust names where Prometheus conventions require it, but document every change.

Do not use incident titles, descriptions, arbitrary URLs, error messages, or timestamps as labels. Incident details belong in structured logs. A bounded severity label is acceptable if normalized to a documented enum.

Use `psp` and stable endpoint/component IDs as identifiers. Do not use mutable display names as primary labels.

## Statuspage adapter

Use documented Statuspage API v2 endpoints such as:

- `/api/v2/summary.json`
- `/api/v2/incidents.json`
- `/api/v2/scheduled-maintenances.json`

Requirements:

- Handle pagination or explicitly document where it is not applicable.
- Normalize component states and incident impact levels.
- Handle malformed responses, timeouts, non-2xx responses, and partial data.
- Do not mark a PSP operational merely because the status page returned HTTP 200.
- Preserve the last successful result when a later poll fails, while exporting freshness and source-health metrics.
- Make stale data detectable.

## Blackbox integration

Use Blackbox Exporter through:

```text
/probe?target=<encoded-target>&module=<encoded-module>
```

Parse its Prometheus response and map relevant probe metrics into the normalized `psp_probe_*` namespace.

Requirements:

- Apply strict request timeouts.
- Validate configured module and target values.
- Distinguish a failed probe from Blackbox Exporter itself being unavailable.
- Do not blindly copy every Blackbox metric or label.
- Preserve only the bounded metrics needed by the unified model.
- Correctly URL-encode all query parameters.
- Add tests with a fake Blackbox Exporter server.

## Prometheus rules

Provide example recording rules for:

```prometheus
psp:observed_unavailable
psp:declared_unavailable
psp:unannounced_failure
psp:confirmed_incident
psp:status_source_stale
```

Provide alerts for:

- Observed PSP failure sustained for a configurable period
- Observed failure without an official incident
- Confirmed PSP incident
- Official status source unavailable or stale
- Probe collection unavailable
- TLS certificate nearing expiry
- Scheduled maintenance approaching

Avoid alerting on one isolated failed poll. Include reasonable `for` durations and explanatory annotations.

## Reliability and security requirements

- Bound all HTTP response bodies.
- Set connection, request, and idle timeouts.
- Use retry with bounded exponential backoff only where appropriate.
- Add jitter to avoid synchronized polling.
- Make all background operations context-aware.
- Avoid unbounded goroutines, queues, labels, or caches.
- Ensure cache reads and writes are race-safe.
- Preserve last-known-good data and expose its age.
- Do not log credentials, authorization headers, or sensitive URLs.
- Support graceful shutdown.
- Ensure `/metrics` remains available when individual PSP sources fail.
- Run tests with the Go race detector where practical.

## Testing

Include:

1. Configuration validation tests
2. Statuspage response normalization tests
3. Statuspage timeout and malformed-response tests
4. Blackbox metric parsing tests
5. Blackbox unavailable versus probe-failed tests
6. Cache concurrency tests
7. Prometheus collector tests
8. HTTP endpoint tests
9. An integration test using local fake HTTP servers

Use deterministic tests; do not depend on live PSP endpoints.

## Local development

Provide:

- `Makefile` or repository-equivalent commands
- Formatting, linting, tests, race tests, build, and Docker targets
- Multi-stage Dockerfile running as a non-root user
- Docker Compose with:
  - `psp_exporter`
  - Prometheus Blackbox Exporter
  - Prometheus
  - Fake PSP/status endpoints where useful
- Example dashboards are optional for the MVP

Expected commands should be approximately:

```bash
make fmt
make lint
make test
make test-race
make build
docker compose up --build
```

## Documentation

The README must explain:

- Architecture and signal model
- Why declared and observed status remain separate
- Configuration reference
- Metrics reference
- Adding a PSP
- Adding a future status adapter
- Running locally
- Prometheus integration
- Example PromQL and alerts
- Staleness and last-known-good behavior
- Security considerations
- Known MVP limitations

Also add an architecture decision record explaining why Blackbox Exporter is delegated to rather than reimplemented.

## Definition of done

The MVP is complete when:

- It builds successfully.
- All tests pass.
- Formatting and lint checks pass.
- The race detector reports no issue in covered paths.
- Docker Compose starts the complete local stack.
- Prometheus successfully scrapes `psp_exporter`.
- A fake Statuspage incident changes declared-status metrics.
- A failing fake API changes observed probe metrics.
- The two signals remain independently visible.
- Recording rules produce the expected unified states.
- No unbounded or high-cardinality metric labels are introduced.
- Documentation is sufficient for another engineer to add a PSP.

## Working style

- Implement the smallest production-quality vertical slice first.
- Keep commits or logical changes small and reviewable.
- Do not add speculative abstractions beyond the adapter boundaries.
- Do not silently ignore errors.
- Do not call real PSP production endpoints in automated tests.
- Explain material trade-offs in code comments or ADRs, not obvious syntax.
- After implementation, run the complete validation suite and report:
  - files changed;
  - commands run;
  - test results;
  - remaining limitations;
  - recommended next development step.