# Enhanced workspace validation

Validated locally on 31 October 2026. These results describe the implementation in this checkout, not the published v0.1.0 release.

## Automated checks

- Go race tests and `go vet`: pass, including lifecycle, namespace isolation, optimistic writes, backup/restore, writer locking, budget persistence, model decision validation, shadow-to-apply reuse, and webhook cancellation/retry cases.
- Frontend: 21 unit tests, TypeScript production build, and formatting checks pass.
- Browser: 13 tests pass, including existing silence workflows against an isolated real Alertmanager, tenant read-only behavior, coverage gaps, workflow edits, pagination, webhook deep links, and reviewable shadow decisions.
- Binary smoke test: creates a synthetic incident from a local source, assigns ownership and review time, rejects stale versions, restarts the app, and verifies persisted identity, acknowledgement, review date, and audit history.
- Alertmanager compatibility: 0.27.0 at the root path and 0.34.1 under a subpath pass existing API, silence, suppression, CSRF, read-only, and embedded-asset checks.
- Helm: stateless and enhanced templates render; enhanced mode uses one replica, a retained PVC, and Recreate strategy. Multiple enhanced replicas are rejected.
- Container: Grype scan with fixed high-severity failures enabled reports no vulnerabilities. Image builds; starts non-root with a read-only root filesystem, dropped capabilities, and a separately writable data mount. An unavailable upstream is explicit.
- Cross-compilation: Linux amd64/arm64 and macOS amd64/arm64 compile with CGO disabled.
- Dependency checks: npm audit reports no known vulnerabilities. With patched Go 1.27.3, govulncheck reports no vulnerable calls or imported packages; one module-level advisory concerns code not reached by the app.

The new storage dependency exposed an old local build toolchain during verification. The module now selects Go 1.27.3; CI/container builds already use the patched Go 1.27 line.

## Live read-only checks

The new analytics collector queried the existing cluster through authenticated monitoring ingress, without changing the cluster or sending data to Jev:

- Seven-day window: 27 namespace rows, approximately 99.91% source coverage, and explicit incomplete status for nine minutes of missing coverage.
- Thirty-day window: 30 namespace rows, approximately 65.54% source coverage; unavailable history remains unknown.
- The seven-day and thirty-day API responses completed in approximately 0.9s and 2.6s respectively during the initial check.
- The enhanced read-only preview collected 16 deterministic incidents from current alerts. Workflow ages begin at local first observation, not reconstructed historical incident dates.

These are point-in-time observations, not a benchmark guarantee. Private response samples stay in ignored local build artifacts. Public screenshots use synthetic data only.

## Explicit limits of validation

Jev's transport, option validation, abstention, budget gates, and application safeguards are tested with fixtures. Live model accuracy on cluster incidents has **not** been measured: no API key was supplied/enabled and no cluster alert data was sent to an external model. Start with shadow evaluation before enabling automatic grouping/ranking.

Webhook delivery was tested with an isolated local receiver, not a real team's notification endpoint. HTTP acceptance is not proof of human notification. No production deployment, image publication, notification route changes, or live silence changes were performed for this feature implementation.

CI and the release workflow now run the persistent workspace smoke test and enhanced Helm rendering alongside their existing tests and image scan. Remote GitHub workflows have not been run for these uncommitted changes.


## Resource and workload extension — 1 November 2026

The existing alert analytics and incident workspace are retained. New resource and workload endpoints were checked against the cluster through the same read-only monitoring ingress.

Final regression checks pass: Go race tests and vet, 27 frontend unit tests, 18 browser tests (including real Alertmanager silence workflows), production build, formatting, Helm rendering, persistent-workspace restart smoke, and Alertmanager 0.27.0/0.34.1 compatibility. Live browser checks covered overview, resources, workloads, analytics, and incidents with no page errors; desktop/mobile checks found no horizontal overflow.

- Workload inventory returned 947 pods across 63 namespaces: 603 ready, two not ready, nineteen failed, and 323 completed. Independent Prometheus queries confirmed the phase/readiness totals. Service inventory returned 514 Services.
- Twenty-four-hour resources returned 63 namespace rows, nine nodes, and 400 individual quota/resource entries. Node-exporter usage was approximately 17.5 busy CPU cores and 230 GiB used host memory, against 138 allocatable cores and approximately 461 GiB allocatable memory. These figures change continuously and host usage differs from container usage.
- Seven-day resource history returned 65 namespace rows in approximately fourteen seconds, without query failures after splitting expensive request-history queries into daily chunks. This remains a point-in-time observation, not a latency guarantee.
- EndpointSlice association labels and service probes are absent on this cluster. Services therefore show unknown endpoint readiness and unmeasured reachability, rather than invented availability.
- Some PVC statistics, limits, and historical series are incomplete. Missing figures remain explicit; completed jobs do not count as failing pods.

Live data is kept in ignored local artifacts. Public examples remain synthetic. The local preview is read-only; no production rollout or Kubernetes resource changes were performed.

## Portability and correctness review — 1 November 2026

Fresh review covered upstream access and tenant boundaries, historical queries and caches, workload interpretation, persistence/triage/webhook safeguards, frontend states, and deployment assumptions. Fixed:

- Default source coverage no longer depends on a Helm-generated scrape job name. A real promtool evaluation verifies a differently named self-monitoring job, a failed scrape, and absent self-monitoring data.
- Collector caches now retain encoded immutable responses, each limited to 16 MiB and 64 entries. Regression tests cover byte eviction and mutation isolation.
- Probe association requires explicit target namespace/service labels; exporter scrape labels cannot establish target identity. Tenant target filters apply before querying. Stale observed targets prevent a misleading all-success result.
- Conflicting endpoint readiness or service mappings, and stale-only EndpointSlice mappings, remain unknown.
- Controller attention filtering considers available replicas as well as ready replicas.
- Missing limit metrics are described as unobserved limits, rather than proven unconfigured limits.

Validation passed: Go race tests, vet, 28 frontend unit tests, 23 browser tests, production build, formatting, Helm validation, and persistent-workspace restart smoke. Updated read-only cluster checks returned seven-day analytics with approximately 99.90% source coverage, resource data for 63 namespaces, and current workload data for 63 namespaces, with no query failures during that check.

This does not establish compatibility with every exporter or monitoring topology. The basic Alertmanager UI remains independent of Kubernetes exporters. Enhanced grouping and resource/workload aggregation require a single-cluster source; federated sources must be filtered before reaching the app. Optional service probes require the explicit labels documented in resources-and-workloads.md. No production rollout or model requests were performed.
