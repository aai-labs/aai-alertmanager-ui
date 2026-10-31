# Alertmanager compatibility

The application uses Alertmanager's **v2 HTTP API**. It replaces the browser interface, not the Alertmanager process or its notification pipeline. Prometheus continues sending alerts to Alertmanager directly.

## Verified matrix

The following integration runs passed on **2026-10-31** against the official Linux AMD64 container images:

| Alertmanager | Hosting path | Result |
| --- | --- | --- |
| 0.27.0 | `/` | Passed |
| 0.34.1 | `/attention/` | Passed |

## Automated integration checks

The integration suite starts isolated, official Alertmanager containers and checks:

- Reading a real active alert through the UI backend.
- Previewing equality and anchored regular-expression matchers, including invalid expressions.
- Creating a silence, listing it, and observing suppression of the alert.
- Editing a silence and expiring the returned silence ID. Alertmanager may replace a silence with a new ID when it is edited.
- Observing the alert become active again after expiration.
- Rejecting cross-origin mutation requests and read-only mutations.
- Serving embedded JavaScript/CSS assets at the root and under a URL prefix, including bookmarked-page fallback.

Run after building the application:

```sh
make build
python3 scripts/compatibility.py --binary .build/alertmanager-ui
```

Python 3.9+ and a running Docker daemon are required. The default matrix is Alertmanager **0.27.0** and **0.34.1**. Tests download official images from `quay.io/prometheus/alertmanager`, use random loopback ports, and remove their containers afterward. They do not connect to Kubernetes or your existing Alertmanager.

To check another release:

```sh
python3 scripts/compatibility.py --binary .build/alertmanager-ui --versions 0.34.1
```

This is a tested-version matrix, not a guarantee that every intervening release or proxy configuration behaves identically. Run the checks against the release you deploy when adding a new supported version.

## Boundaries

- One upstream Alertmanager URL per application instance. An existing load-balanced service may supply that URL; this UI does not discover or manage Alertmanager peers.
- Alertmanager remains the authority for alert state and silences. The UI's grouping does not alter notification grouping or inhibition rules.
- Namespace-scoped tenant access is implemented by this application's server and requires the documented trusted authentication proxy setup. Alertmanager itself does not provide namespace authorization.
- Optional Prometheus enrichment uses the Prometheus HTTP API. It is not required for alert browsing or silence management, and it is outside the Alertmanager container matrix above.
- A reverse proxy must preserve the configured base path and provide the expected authenticated identity when tenant mode is enabled.

See the [Alertmanager API](https://github.com/prometheus/alertmanager/blob/main/api/v2/openapi.yaml) and [Prometheus security model](https://prometheus.io/docs/operating/security/) for upstream behavior.
