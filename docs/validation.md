# MVP validation — 2026-09-18

Implementation and review completed in `/Users/alaindeleglise/repos/pssst`, on `feat/pssst-mvp`. Changes remain uncommitted and have not been pushed. The supplied input plan is preserved in `docs/specification.md`; decisions are documented in `docs/superpowers/specs/2026-09-18-pssst-design.md`.

## Results

| Check | Result |
| --- | --- |
| `make fmt-check lint test test-race build rules-test` | Passed on the final Go source and rules. Formatting, `go vet`, unit/integration tests, race detector, both binaries, and all rule fixtures passed. |
| `go mod verify` | All modules verified. |
| `podman build --target exporter -t localhost/pssst:dev .` | Passed; non-root scratch image includes CA roots. |
| `podman build --target fake-psp -t localhost/pssst-fake:dev .` | Passed. |
| `podman compose config --quiet` | Passed. |
| Container `promtool check config /etc/prometheus/prometheus.yml` | Valid Prometheus configuration and 13 rules. |
| `make smoke` | Passed with the real local Podman engine, Blackbox 0.28.0 and Prometheus 3.14.0. |
| Smoke isolation regression | Passed while a separate normal demo was running with both failure switches set. That state remained unchanged after smoke teardown. |
| Compiled binary process check | `/readyz`, `/metrics`, JSON logs, SIGTERM cancellation and exit code 0 passed using a temporary local config. |
| Grafana dashboard | JSON parsed; all ten panel queries validated using `promtool check rules` after resolving dashboard selectors. |
| Independent code review | Core review and delivery review completed; all actionable findings fixed and re-reviewed. |

The final smoke test verified a successful Prometheus scrape, initially healthy signals, a declared-only incident while the real Blackbox probe stays healthy, API failure while official status is healthy, `psp:unannounced_failure`, `psp:confirmed_incident`, and recovery. Automated tests never used real PSP production endpoints.

The collection helper rule is evaluated before its consumers. A new regression fixture failed with the original rule order, then passed with the corrected order; it covers healthy startup and immediate transition to an unknown endpoint. Rule tests specify group evaluation order, and ten repeated rule-suite runs passed after an earlier cross-group ordering ambiguity was corrected.

## Review changes

- Incomplete status responses fail atomically. Contradictory component failures cannot produce a healthy overall declaration.
- Remote incident IDs are constrained before entering logs; HTTP status metrics accept only zero or valid HTTP codes.
- Failed probes differ from unavailable probe collection. Partly unknown PSPs cannot produce a healthy recording state; known failures remain visible even when another endpoint is unknown.
- Smoke uses a separate Compose project and ports, with no HTTP mutation in its failure-cleanup handler.
- The example DNS module includes a required query name; the real Blackbox startup caught the initial omission.

## Environment and remaining limits

The local runtime was Podman (client 6.1.1, machine 5.1.2); `podman compose` uses the installed Compose provider against the Podman socket. No Docker daemon was used for local builds or execution. The smoke project and volume were removed. The separate demo containers were stopped, with their Prometheus volume retained after automatic approval review rejected deleting that data. The default `make down` now preserves the normal demo volume.

Portainer at `192.168.1.250:19443` refused TCP connections. Its token was retrieved from the specified macOS Keychain entry without displaying it or saving it to the repository. No NAS services/configuration were changed. `examples/prometheus-existing.yml`, the recording/alert rules, and the Grafana dashboard are prepared for integration. The dashboard has not been imported into the NAS Grafana, whose version and datasource configuration could not be inspected.

The MVP intentionally has in-memory state and static configuration requiring restart, no history ingestion or generic HTML scraping, and no PSP-specific business checks. No real PSP production URL is assumed to be a health endpoint. The next development step is to select real PSP/component IDs and reviewed Blackbox modules, then connect the exporter to the existing NAS Prometheus and import the dashboard once the Portainer endpoint is reachable.

## Added files

- `.dockerignore`
- `.github/workflows/ci.yml`
- `.gitignore`
- `Dockerfile`
- `Makefile`
- `README.md`
- `cmd/fake-psp/main.go`
- `cmd/fake-psp/main_test.go`
- `cmd/fake-psp/test_helpers_test.go`
- `cmd/psp-exporter/main.go`
- `compose.yaml`
- `deploy/blackbox/blackbox.yml`
- `deploy/grafana/pssst.json`
- `deploy/prometheus/prometheus.yml`
- `deploy/prometheus/pssst-rules-test.yml`
- `deploy/prometheus/pssst.yml`
- `docs/adr/0001-blackbox.md`
- `docs/specification.md`
- `docs/superpowers/plans/2026-09-18-pssst.md`
- `docs/superpowers/specs/2026-09-18-pssst-design.md`
- `docs/validation.md`
- `examples/prometheus-existing.yml`
- `examples/pssst.compose.yml`
- `examples/pssst.production.yml`
- `go.mod`
- `go.sum`
- `internal/blackbox/client.go`
- `internal/blackbox/client_test.go`
- `internal/blackbox/model.go`
- `internal/cache/cache.go`
- `internal/cache/cache_test.go`
- `internal/collector/collector.go`
- `internal/collector/collector_test.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/httpclient/client.go`
- `internal/scheduler/scheduler.go`
- `internal/scheduler/scheduler_test.go`
- `internal/server/server.go`
- `internal/server/server_test.go`
- `internal/status/none/none.go`
- `internal/status/none/none_test.go`
- `internal/status/status.go`
- `internal/status/statuspage/client.go`
- `internal/status/statuspage/client_test.go`
- `scripts/smoke.sh`
