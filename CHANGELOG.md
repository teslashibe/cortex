# Changelog

## v1.0.0

First public release of Cortex under the MIT license.

- Go library with `Observe`, `Recall`, and `Consolidate` operations.
- PostgreSQL storage with embedded migrations and user-scoped observation idempotency.
- Strict model-output validation, source provenance, and retained contradiction history.
- Atomic memory changes, exact observation acknowledgement, and recovery after interrupted consolidation.
- Replaceable model interface and an HTTP client for the documented Open Agent API setup.
- Local HTTP service, export import commands, and synthetic examples.
- Automated PostgreSQL regression tests, race checks, vulnerability checks, and secret scanning.

Cortex's HTTP service trusts local callers. Applications own authentication,
authorization, and source consent. Recall uses text matching; there is no vector
search, background scheduler, or automatic source-permission revocation.
