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

## What PSSST reads and sends

See [Security and what it reads](README.md#security-and-what-it-reads) in the
README. It has no telemetry and makes no request the inventory does not name.
