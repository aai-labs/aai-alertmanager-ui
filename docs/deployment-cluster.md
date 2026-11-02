# First cluster rollout

Deploy the UI alongside Alertmanager, validate it on a separate protected route, and only then change the route people use. Keep the original Alertmanager UI reachable for operators during rollout.

## Prepare

1. Build the image or choose a published immutable version.
2. Find the existing Alertmanager and optional Prometheus service addresses. Use internal service URLs to avoid ingress authentication redirects between services.
3. Create a small Helm values file with those addresses, the image tag, `readOnly: true`, and a separate ingress hostname such as `alerts-preview.example.com`.
4. Attach the existing operator authentication middleware and TLS configuration. Shared BasicAuth is sufficient for operator mode; it does not establish namespace isolation.
5. Review `helm template` output before applying it. In repositories where cluster changes must be performed by the operator, hand over the rendered change and commands instead of applying them automatically.

No changes are needed to Prometheus rule evaluation, Alertmanager routes, receivers, persistence, or its existing ingress. Do not redirect Prometheus alert delivery to the UI.

## Validate

- Compare distinct alert counts with Alertmanager, including suppressed alerts. Receiver duplication should not inflate the UI count.
- Expand a known alert and verify the original labels, severity, state, and annotations.
- Confirm a critical infrastructure fixture is prioritized in a non-production test instance; do not generate a production outage just to test the interface.
- Verify unknown alert names remain visible and collapsed sections show their counts.
- Test an unavailable upstream in a test instance: stale data must be marked, not reported as recovery.
- If Prometheus is enabled, verify pending alerts and rule values independently of Alertmanager. Loss of Prometheus must not prevent alert browsing.
- Test subpath hosting if used, mobile layout, and authenticated access from the intended user network.

After read-only validation, enable operator silence controls and create a narrow, short-lived test silence for a designated test alert. Verify it in the original Alertmanager UI, edit it, then expire it. Do not silence production alerts as a deployment test.

## Tenant access is a separate step

Enable tenant mode only after configuring authenticated identities and explicit namespace grants. The proxy must replace caller-supplied identity headers. Restrict network access to the application's listener so clients cannot bypass the trusted proxy, and set the trusted proxy CIDRs precisely. Do not trust a broad cluster CIDR if unrelated workloads can send requests with arbitrary identity headers.

Test both permitted and forbidden namespaces with direct API requests, not only the dropdown. Ensure tenant users cannot access the original Alertmanager or Prometheus endpoints to bypass the UI's restrictions. Tenants are read-only in this release.

## Cut over and roll back

Once validated, update the user-facing ingress route to the new UI while retaining an operator-only route to the original interface. The Alertmanager API address used by Prometheus and automation must remain unchanged.

If the UI fails, revert its ingress route or Helm release. Alertmanager continues receiving alerts and delivering notifications throughout. Previously created silences remain in Alertmanager and must be expired there if needed.

## Publish a release

Merging into `main` automatically publishes after the **CI** push workflow succeeds. Pull requests, forks, and failed/cancelled CI runs cannot publish. Release checks out that exact tested commit, runs its checks again, and scans the image before publication.

The serialized **Release** workflow reserves the next patch tag (`v0.1.0` → `v0.1.1`). An existing tag for the same commit is reused on retries. It publishes:

- Linux amd64/arm64 images at `ghcr.io/aai-labs/aai-alertmanager-ui:VERSION` and `:latest`.
- The matching OCI Helm chart with its default image version set to that release.
- A public GitHub release with Linux/macOS amd64/arm64 binaries, Helm archive, checksums, software inventory, and artifact attestations.

This uses the successful-CI `workflow_run` event, rather than relying on a bot-created tag to start another workflow. No personal access token is required. The workflow needs repository contents/package write and attestation permissions. First-time GHCR packages may require setting public visibility; verify anonymous pulls.

To recover a failed publication, rerun the failed **Release** run, or dispatch **Release** from `main` with the reserved tag. Leave the tag empty to release the current main commit. A completed public release is skipped. Partial publication reuses the same commit/tag; do not move an existing tag to different code. The `latest` tag tracks the last publication; use an immutable version for deployments.

The downloadable `image-linux-amd64.spdx.json` describes the scanned Linux amd64 image. Multiarch images also carry BuildKit SBOM and provenance attestations. Images/chart publication precedes the final public GitHub release, so partial failure may leave packages published. Production rollout remains an operator action.

## Enhanced workspace preview

`deploy/cluster-preview-values.yaml` supplies this cluster's monitoring service names, a single replica, a persistent volume, and read-only mode with ingress disabled. Use it only with a newly published version containing enhanced mode; `v0.1.0` does not.

Render for review before deployment (from this UI repository, after setting `RELEASE_VERSION` to the new published semver):

```bash
helm template alerts-preview charts/aai-alertmanager-ui \
  --namespace monitoring -f deploy/cluster-preview-values.yaml \
  --set-string image.tag="${RELEASE_VERSION:?Set the newly published release version}"
```

Production installation is an operator action and should be incorporated into the infrastructure repository's existing Helmfile workflow. Do not expose a second unprotected ingress. Validate analytics coverage and first-observed incident ages before enabling workflow writes. Jev and webhook settings are intentionally absent from the preview values; add their secret-backed configuration only after review.
