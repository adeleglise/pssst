# ADR 0001: Delegate network probing to Prometheus Blackbox Exporter

## Status

Accepted, 2026-09-18.

## Context

PSSST needs observed HTTP, TCP, DNS, and TLS evidence alongside declared PSP status. Implementing those protocol probes in the exporter would duplicate mature Blackbox behavior and add DNS, TLS, redirect, authentication, timeout, and certificate policy to a service whose primary role is normalization and correlation.

## Decision

PSSST calls a configured Blackbox Exporter `/probe` endpoint asynchronously. It sends the configured module and target with URL encoding, applies a bounded request deadline, rejects invalid exposition, and accepts only unlabelled scalar `probe_success`, `probe_duration_seconds`, optional HTTP status, and optional earliest certificate expiry. It does not copy arbitrary Blackbox metrics or labels.

The Blackbox response itself and the probe result are separate facts. A valid response with `probe_success 0` is an observed failure. Transport failure, non-2xx response, oversized response, or invalid exposition is failed collection, so the exporter preserves the last known probe result and exports collection freshness. Prometheus rules decide how those independent facts affect alerts.

## Consequences

Blackbox configuration remains an operational dependency and owns protocol-specific modules, authentication, DNS query details, and network policy. PSSST has a smaller attack surface and low-cardinality metric contract, while operators retain familiar Blackbox modules. The local Compose demo pins `quay.io/prometheus/blackbox-exporter:v0.28.0`; its modules are examples and production operators must review them for each PSP.
