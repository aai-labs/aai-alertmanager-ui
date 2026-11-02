# Task-to-document map

Read only the routes relevant to the task. Exact APIs, queries, migrations, and tests remain authoritative in source.

| When working on | Read first | Then inspect |
| --- | --- | --- |
| Backend boundaries and authorization | [Code guidelines](../CODE_GUIDELINES.md), [configuration](configuration.md) | `internal/server`, `internal/config` |
| UI behavior, filters, charts, loading states | [Web guidelines](../WEBAPP_GUIDELINES.md), [prioritization](prioritization.md) | `web/src`, `web/e2e` |
| Tests, regression evidence, compatibility | [Testing](../TESTING.md), [compatibility](compatibility.md), [validation](enhanced-validation.md) | `.github/workflows`, `scripts`, Go tests, `web/e2e` |
| Analytics, workflow, Jev, reviews, backups | [Enhanced mode](enhanced-mode.md), [optional-workspace decision](adr/2026-11-02-optional-incident-workspace.md) | `internal/server`, `internal/incidents`, `internal/triage` |
| Resource, quota, pod, controller, service observations | [Resources and workloads](resources-and-workloads.md) | `internal/server/resources.go`, `workloads.go`, `web/src/ClusterWorkspace.tsx` |
| Install, configure, package, deploy, release | [Installation](installation.md), [configuration](configuration.md), [deployment](deployment-cluster.md) | `charts`, `Dockerfile`, `.github/workflows` |
| Bootstrap or documentation structure | [Bootstrap refresh](../INIT.md), [documentation blueprint](DOCUMENTATION_BLUEPRINT.md) | Existing routed pages and their source/tests |
