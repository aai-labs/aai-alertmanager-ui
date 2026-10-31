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

1. Ensure CI passes, review screenshots and documentation for private data, and review the dependency scan.
2. Tag the reviewed commit with a version such as `v0.1.0` and push the tag.
3. Run the **Release** workflow manually with that existing tag.
4. The workflow tests again, scans the image, publishes versioned images and the OCI Helm chart, and creates a **draft** GitHub release with binaries, checksums, an SBOM, and provenance.
5. Review the draft, set the GHCR image/chart packages to public if this is their first publication, verify anonymous pulls, and publish the release.

The downloadable `image-linux-amd64.spdx.json` describes the locally scanned Linux amd64 image. The published multiarch image also carries BuildKit-generated SBOM and provenance attestations for its platform builds.

The workflow publishes packages before creating the draft release. Running it is the publication action; creating a tag alone does not publish packages. It intentionally does not deploy to a cluster. If publication partially succeeds, inspect the completed steps before rerunning; never overwrite a version already consumed by users with different content.
