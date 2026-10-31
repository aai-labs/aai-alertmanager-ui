# Security

Do not open a public issue containing credentials, private alert payloads, or an exploitable authorization bypass. Use this repository's GitHub **Security → Report a vulnerability** feature when private vulnerability reporting is enabled. If unavailable, open a minimal issue requesting a private reporting channel without disclosing the vulnerability.

## Deployment trust model

Default operator mode trusts every visitor. Put it behind authentication and HTTPS before exposing it. Read-only mode disables silence changes; it does not make alert contents public-safe.

Tenant mode relies on a correctly configured authentication proxy. The proxy must overwrite identity and group headers, the listener must be reachable only from trusted proxies, and upstream monitoring endpoints must not be accessible to tenants. See [configuration](docs/configuration.md).

Upstream alert annotations and labels are untrusted content. They are rendered as text; only HTTP(S) runbook URLs are linked. The server does not expose a general-purpose Prometheus query proxy or arbitrary upstream endpoints.

Use supported releases, immutable image tags/digests, and the CI dependency/container scan results. A release candidate is not a promise of a completed external security audit.
