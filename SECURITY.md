# Security

## Reporting a vulnerability

Report it privately through GitHub: **Security → Report a vulnerability** on
[adeleglise/pssst](https://github.com/adeleglise/pssst/security/advisories/new).
Do not open a public issue. Expect an acknowledgement within a week; a fix
ships as a patch release with a changelog entry crediting you, unless you
prefer otherwise.

Only the latest release is supported.

## In scope

Anything that lets a remote status page or probe target influence PSSST beyond
the value of its own signal: a label or log field built from remote text,
unbounded memory or cardinality, a request PSSST makes that the inventory did
not ask for, a way to make an unknown or absent signal export as healthy.

A provider's status page reporting the wrong status is not a vulnerability in
PSSST; that disagreement is what PSSST exists to show.

Also in scope: a default deployment that exposes more than the README's
security section says it does.

## Exposure

`/metrics`, `/healthz` and `/readyz` are unauthenticated plain HTTP, and
Blackbox Exporter answers any probe request it receives. Neither is a
vulnerability in PSSST; both belong on the monitoring network. The Kubernetes
manifests and the Helm chart restrict Blackbox to PSSST's own pods with a
NetworkPolicy, which needs a CNI that enforces it. Put TLS or authentication
for the exporter in a proxy or sidecar.

## What PSSST reads and sends

See [Security and what it reads](README.md#security-and-what-it-reads) in the
README. It has no telemetry and makes no request the inventory does not name.
