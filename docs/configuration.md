# Configuration

The only required production setting is `ALERTMANAGER_URL`. The application does not need Kubernetes API permissions or its own database. Keep Alertmanager running: it remains responsible for alert delivery, notification routing, inhibition, and silence storage.

## Environment

| Variable | Default | Purpose |
| --- | --- | --- |
| `ALERTMANAGER_URL` | Required | Existing Alertmanager HTTP(S) address, including any upstream path prefix. |
| `PROMETHEUS_URL` | Unset | Optional Prometheus HTTP(S) address for pending alerts and current rule values. |
| `LISTEN_ADDR` | `:8080` | Server bind address. |
| `BASE_PATH` | Empty | Public path prefix, for example `/alerts`; route the entire prefix to this application without stripping it. |
| `READ_ONLY` | `false` | Disable silence creation, editing, and expiration for everyone. |
| `CONFIG_FILE` | Unset | Path to the optional YAML file below. |
| `DEMO` | `false` | Use fictional in-memory alerts instead of upstream services; all mutations are disabled. |

Environment variables override the corresponding file setting. Boolean values must parse as `true` or `false` (Go boolean spellings are accepted). Unknown YAML keys and invalid settings fail startup. Configuration and mounted credentials are read at startup; restart after changing them.

`/healthz` and `/readyz` are always available at the server root, even with `BASE_PATH`. Readiness indicates the application can serve requests, not that the remote monitoring system is healthy. Upstream connection failures appear in the UI and do not cause Kubernetes to restart a useful diagnostic interface.

## Optional YAML

```yaml
alertmanager:
  url: http://alertmanager.monitoring.svc:9093
  # bearerTokenFile: /var/run/secrets/upstream/token
  # caFile: /var/run/secrets/upstream/ca.crt
  # basicAuth:
  #   username: alerts-ui
  #   passwordFile: /var/run/secrets/upstream/password
prometheus:
  url: http://prometheus.monitoring.svc:9090
  # Same authentication and CA options as Alertmanager.
listenAddr: ':8080'
basePath: ''
readOnly: false
priorities:
  - name: Platform storage
    match:
      alert_category: core-cluster-health
    category: Core infrastructure
  - name: Checkout availability
    match:
      alertname: CheckoutUnavailable
    category: Availability
    displayName: Checkout unavailable
```

Use Kubernetes Secrets or equivalent secret files for credentials. Do not put credentials into URLs. TLS verification is always enabled; custom CA certificates extend the system trust store. Basic and bearer authentication are mutually exclusive. Redirects from upstream services are not followed, so configure their final address directly.

### Priority overrides

`priorities` is an ordered list. Each rule requires a human-readable `name`, a nonempty map of exact label `match` values (all must match), and a `category`. `displayName` is optional. The first matching override takes precedence over the bundled alert-name presets. These are presentation rules, not Alertmanager notification routes or inhibition rules.

Supported categories:

- `Core infrastructure`
- `Critical`
- `Availability`
- `Job failures`
- `Capacity`
- `Informational`
- `Other active alerts`

Critical severity remains prominent, and suppression remains explicit. Namespaces and unfamiliar alerts are not assigned importance by guessing their names. See the UI's categorization explanation and the project README for default presentation behavior.

## Optional namespace-restricted access

Without tenant mode, every visitor is a trusted operator (or read-only operator when `READ_ONLY=true`). Protect the application with your existing authentication proxy and network controls before exposing it.

Tenant mode requires a trusted authentication proxy that verifies identity. Configure that proxy to **remove incoming identity/group headers and replace them with verified values**. The application checks the direct connecting IP, not `X-Forwarded-For`, against `trustedProxyCIDRs`.

```yaml
tenancy:
  enabled: true
  identityHeader: X-Authenticated-User
  groupsHeader: X-Authenticated-Groups
  trustedProxyCIDRs:
    - 10.42.1.12/32 # Example only: use the actual, restricted proxy addresses.
  users:
    alice@example.org:
      - storefront
      - payments
    bob@example.org:
      - analytics
  groups:
    analytics-readers:
      - analytics
  operatorUsers:
    - oncall@example.org
  operatorGroups:
    - monitoring-operators
```

`users` and `groups` grant exact namespace names, without wildcards. The optional groups header contains comma-separated group identities. Grants from the user's identity and groups are combined. Listed operator users/groups receive full operator access; otherwise all tenant access is read-only. Missing identity, an untrusted connecting IP, or no matching namespace grant results in HTTP 403. Demo mode cannot be combined with tenant mode.

Tenant filtering is enforced on the server for alerts and Prometheus enrichment. Priority rules with an explicit unauthorized namespace are removed from tenant context; shared rules without a namespace are visible to all authorized users, so do not put confidential metadata into shared rule names or match values. Namespace-less alerts are excluded. Tenants cannot list or change global silences or access upstream configuration/status endpoints. Suppression state remains visible, but IDs of silences and inhibiting alerts and receiver names are removed from tenant alert responses.

Use network policy/firewalls to make this server reachable only through the authentication proxy, and keep Alertmanager and Prometheus themselves inaccessible to tenants. Do not trust an entire cluster or public address range merely to make proxy checks pass: another caller inside that range could forge identity headers. A namespace dropdown in operator mode is a convenience filter, not an authorization mechanism.

## API behavior and limitations

The browser uses the UI server's same-origin `/api/*` endpoints. These are an application API, not a replacement Alertmanager ingestion endpoint. Prometheus and other alert producers should continue sending alerts directly to Alertmanager.

- `GET /api/context`: safe UI capabilities, current identity, permitted namespaces, and presentation rules.
- `GET /api/alerts`: current Alertmanager v2 alerts, deduplicated by fingerprint and scoped to the current identity.
- `GET /api/prometheus`: optional Prometheus `/api/v1/alerts` response, scoped to the current identity.
- `GET /api/silences`: operator-only silence list.
- `POST /api/silences/preview`: operator-only matcher preview, including read-only operators. JSON `{ "matchers": [...] }` returns `{ "count": 0, "alerts": [] }`.
- `POST /api/silences`: create or update using Alertmanager v2 silence fields. Follow the returned `silenceID`; Alertmanager may return a new ID when editing.
- `DELETE /api/silences/{id}`: expire a silence.

All paths use `BASE_PATH` when configured. Requests have a 10-second upstream timeout, a 1 MiB mutation-body limit, and a 32 MiB upstream-response limit. Mutation POSTs require JSON; cross-origin and browser cross-site mutations are rejected. Upstream credentials, cookies, and caller-provided query parameters are not forwarded from the browser. Upstream errors return a safe message, not raw backend response bodies.

Silence preview uses Go's RE2-compatible, fully anchored matching, including absent labels as empty strings. Preview counts only currently returned alerts; a silence may also match future alerts. The application has no durable history and cannot infer recovery or infrastructure health from an empty result.
