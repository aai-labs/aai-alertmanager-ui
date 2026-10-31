# Installation

The UI runs alongside an existing Alertmanager. It does not replace the Alertmanager process, notification receivers, rules, or storage. The browser connects to the UI; the UI connects to Alertmanager's v2 API.

Only `ALERTMANAGER_URL` is required. Prometheus is optional. No Kubernetes credentials or database are needed.

## Docker

```bash
docker run --rm -p 127.0.0.1:8080:8080 \
  -e ALERTMANAGER_URL=http://alertmanager:9093 \
  --network monitoring \
  ghcr.io/aai-labs/aai-alertmanager-ui:v0.1.0
```

This example assumes an existing Docker network named `monitoring` with an Alertmanager container named `alertmanager`. Substitute an address and network reachable from the UI container. `localhost` inside a container refers to that container, not your host.

Open [localhost:8080](http://localhost:8080). For public exposure, put the UI behind your existing authenticated reverse proxy and HTTPS. Default operator mode permits all alerts and silence management; it does not implement a login page.

To try synthetic examples without an upstream:

```bash
docker run --rm -p 127.0.0.1:8080:8080 -e DEMO=true \
  ghcr.io/aai-labs/aai-alertmanager-ui:v0.1.0
```

Demo mode is for evaluation, not production monitoring. The image and Helm chart are public: no GitHub account is required to pull them. Publishing requires authenticated write access. Pin a release tag or digest in production. Images support Linux `amd64` and `arm64`; there is no floating `latest` tag.

To build from source:

```bash
git clone https://github.com/aai-labs/aai-alertmanager-ui.git
cd aai-alertmanager-ui
docker build -t aai-alertmanager-ui:local .
```

## Docker Compose

The provided Compose file builds the application locally:

```bash
ALERTMANAGER_URL=http://your-alertmanager:9093 docker compose up --build -d
```

Set `PROMETHEUS_URL` to add pending alerts and rule values. Set `READ_ONLY=true` to disable silence changes. Compose binds to loopback by default. Attach the service to your existing Docker network when using container DNS names.

## Kubernetes / Helm

To use a locally checked-out chart with your own image, build and push the image to a registry your cluster can pull, then supply that image and your existing Alertmanager service URL:

```bash
helm upgrade --install alerts ./charts/aai-alertmanager-ui \
  --namespace monitoring \
  --set image.repository=YOUR_REGISTRY/aai-alertmanager-ui \
  --set image.tag=YOUR_TAG \
  --set alertmanager.url=http://YOUR_ALERTMANAGER_SERVICE.monitoring.svc:9093
```

For the published release, install the OCI chart:

```bash
helm upgrade --install alerts oci://ghcr.io/aai-labs/charts/aai-alertmanager-ui \
  --version 0.1.0 \
  --namespace monitoring \
  --set alertmanager.url=http://YOUR_ALERTMANAGER_SERVICE.monitoring.svc:9093
```

The namespace must already exist. No RBAC, CRDs, or Kubernetes API permissions are installed. The pod runs as a non-root user with a read-only filesystem and no service-account token.

For a private local preview:

```bash
kubectl -n monitoring port-forward service/alerts-aai-alertmanager-ui 8080:8080
```

Ingress is disabled by default. An example values file for an authenticated Traefik route:

```yaml
alertmanager:
  url: http://YOUR_ALERTMANAGER_SERVICE.monitoring.svc:9093
prometheus:
  url: http://YOUR_PROMETHEUS_SERVICE.monitoring.svc:9090
readOnly: true
ingress:
  enabled: true
  className: traefik
  host: alerts.example.com
  annotations:
    traefik.ingress.kubernetes.io/router.middlewares: monitoring-alertmanager-auth@kubernetescrd
  tls:
    - secretName: alerts-tls
      hosts: [alerts.example.com]
```

The middleware and TLS Secret must already exist. The example reuses operator authentication; tenant identity requires additional proxy and application configuration.

Explicit chart values override corresponding configuration-file settings. Leave `readOnly` unset and `prometheus.url` / `basePath` empty to use the file settings. The chart always supplies `alertmanager.url` and the listener port.

Mount an optional configuration file from an existing Secret using `config.existingSecret` and `config.key`. Use `extraVolumes` / `extraVolumeMounts` for CA and credential files and `extraEnv` with `valueFrom.secretKeyRef` for secret environment variables. Do not put credentials in Helm values committed to source control. See [configuration](configuration.md).

## Subpath hosting

Set `BASE_PATH=/alerts`, or Helm `basePath: /alerts`, and forward `/alerts` without stripping the prefix. Set `ingress.path: /alerts` when using the chart. The UI, API, and assets share that prefix. Probe endpoints `/healthz` and `/readyz` remain at the root.

## Standalone binary

Download the archive for your operating system and architecture from a published release, verify its checksum against `SHA256SUMS`, extract it, then run:

```bash
ALERTMANAGER_URL=http://localhost:9093 ./alertmanager-ui
```

The frontend is embedded; no separate web server is required. The default listener is `:8080`. To bind only to loopback, set `LISTEN_ADDR=127.0.0.1:8080`.

## Upgrade and rollback

Keep your Alertmanager untouched. Change only the UI image tag or binary, and check the UI's source status and alert counts after rollout. In Helm, `helm rollback alerts REVISION --namespace monitoring` restores an earlier UI release. Changing UI versions does not undo silences you intentionally created in Alertmanager.

See [cluster rollout](deployment-cluster.md) for the first installation checklist and [compatibility](compatibility.md) for upstream coverage.
