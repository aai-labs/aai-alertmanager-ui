# AGENTS.md

## Project context

- **Stack / versions** — Go (`go.mod` selects 1.27.3), React 19, TypeScript, Vite; CI uses Node.js 24. Node minimum: 22.12. No Next.js or Python application backend.
- **Package manager** — npm with `web/package-lock.json`; use `npm ci --prefix web`.
- **Commands** — `make build` builds and embeds the frontend; `DEMO=true .build/alertmanager-ui` runs synthetic data; `npm run dev --prefix web` runs Vite with a backend on port 8080.
- **Checks** — `go test -race ./...`; `go vet ./...`; `npm test --prefix web`; `npm run build --prefix web` (includes TypeScript); `npm run format:check --prefix web`.
- **Boundary tests** — after `make build`, `npm run test:e2e --prefix web`; `python3 scripts/enhanced-smoke.py --binary .build/alertmanager-ui`; `make helm-check`; `make compatibility`.
- **Focused tests** — `go test ./internal/server -run TestName`; `npm test --prefix web -- src/cluster.test.ts`; `npm run test:e2e --prefix web -- cluster.spec.ts`.
- **Embedded UI** — use `make build` for a runnable UI; a bare Go build does not refresh frontend assets. Generated assets and `.build/` are ignored; preserve `internal/server/ui/placeholder.txt`.
- **Optional capabilities** — Alertmanager alone must remain usable and stateless. Prometheus adds analytics/resources; enhanced mode adds SQLite. Keep Jev and webhooks opt-in and server-side.
- **Authorization** — filter tenant namespaces before querying and aggregating; partition caches by authorization scope. UI filters are not access control.
- **Data semantics** — missing metrics are unknown, not zero or healthy. Source severity, incident urgency, condition clearance, and workflow closure are distinct.
- **Portability** — use the documented single-cluster boundary and standard exporter contracts. Do not assume our namespaces, Helm release name, Ceph installation, or scrape job names.

## Retained starter principles

Keep the original design intent when tailoring examples: explicit ownership and immutability, composition, cohesive feature/domain structure, encapsulated invariants, simple control flow, and behavior-focused testing. [CODE_GUIDELINES.md](CODE_GUIDELINES.md) and [TESTING.md](TESTING.md) explain how these apply to Go and React. Stack adaptation must not remove these principles.

## Work loop

1. Read the task-relevant routes in [docs/INDEX.md](docs/INDEX.md), source, and tests. Explain the intended change before substantive work.
2. Make focused changes using [CODE_GUIDELINES.md](CODE_GUIDELINES.md); for UI work also use [WEBAPP_GUIDELINES.md](WEBAPP_GUIDELINES.md).
3. Run meaningful checks from [TESTING.md](TESTING.md), fixing failures introduced by the change. Report actual evidence and remaining limits.
4. Update authoritative feature docs with contract changes and [CHANGELOG.md](CHANGELOG.md) for significant delivered behavior.
5. Follow the user's authorization for commits, pushes, PRs, and merges; do not publish releases or deploy production unless requested.

## Operational guardrails

- Use synthetic fixtures for public screenshots and tests. Keep live monitoring responses, credentials, kubeconfigs, and private local artifacts out of Git and images.
- Cluster validation is read-only unless production changes are explicitly authorized. Respect instructions in the infrastructure repository when accessing it.
- Critical paging and routes remain owned by Alertmanager. Model judgments must not silence alerts, suppress critical notifications, run commands, or cross tenant boundaries.
- Reuse this checkout when suitable. Do not install global plugins, dependencies, or create extra agent instruction files merely because a starter mentions them.
- Read [INIT.md](INIT.md) only for an explicitly requested bootstrap refresh. Keep this entry point short; route detailed guidance through docs.
