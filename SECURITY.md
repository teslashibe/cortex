# Security

Cortex is an embedded library and a trusted-localhost service. Its HTTP routes
do not authenticate callers or authorize their requested user scope. Applications
that embed or proxy Cortex must enforce those decisions before calling it.

The profile tier filter is a display heuristic. It is not a guarantee that a
profile is safe to share. Review generated memories and authorize access in the
application that presents them.

Consolidation sends observations and existing memories to the configured model
endpoint. Choose that endpoint according to your data-handling requirements.
Cortex does not provide encryption at rest or automatic source-permission
revocation. Restrict database access and protect backups separately.

Keep credentials, exports, database dumps, and personal profiles out of source
control. The repository's examples contain synthetic data only.

## Reporting a vulnerability

Use GitHub private vulnerability reporting when it is enabled for this repository.
If it is unavailable, open an issue requesting a private contact route without
including credentials, personal data, or exploit details.

Include the affected revision, configuration, expected behavior, and a minimal
reproduction with synthetic data in the private report.
