# Code guidelines

Adapted from the installed ai-starter baseline for this Go application. User instructions take precedence; MUST denotes an invariant and SHOULD a default.

## Boundaries

- Keep HTTP parsing, authorization, and response mapping in `internal/server`; persistence and workflow transitions belong in `internal/incidents`. Provider transport belongs in `internal/triage`; configuration parsing belongs in `internal/config`.
- Use existing structs, functions, sentinel errors, and explicit dependencies. Do not introduce a framework, inheritance hierarchy, repository abstraction, or API version migration just to satisfy generic starter examples.
- Parse and bound untrusted input at the boundary. Preserve meaningful HTTP errors, including 409 for stale incident mutations. Do not return internal credentials, upstream error bodies, model inputs, or unrelated tenant data.
- Namespace authorization MUST precede upstream queries and aggregation, with defensive response filtering. Namespace-less data and node/cluster aggregates remain operator-only. Cache entries MUST be scoped and bounded by memory and age.
- Use request cancellation and bounded upstream timeouts, sample counts, response sizes, and concurrency. Propagate storage errors; never infer recovery from an upstream failure.

## Workflow and data

- Keep basic mode stateless. Enhanced mode uses the existing pure-Go SQLite store, WAL, migrations, writer lock, and one application replica. Preserve optimistic version checks and transactional outbox updates.
- Pass explicit time into transitions where behavior depends on time. Tests must cover recurrence, stale observations, review revisions, and retries without sleeping for real deadlines.
- Prometheus owns time series. SQLite owns incident/workflow state. Do not copy historical metric samples into the incident database.
- Preserve observed source severity independently of optional model urgency. Operator corrections take precedence; source-critical conditions cannot be downgraded by a model.
- Keep optional external integrations disabled by default. Credentials come from server-side secret files. No arbitrary query, command, retrieval, or model-generated action endpoints.
- Favor portable metric contracts and explicit unknown states over heuristics based on our cluster. Document exporter and single-cluster requirements when relevant.

## Change discipline

Prefer linear control flow, small named functions, composition, and explicit error handling. Explain surprising invariants in comments. Avoid unrelated refactors, unused abstractions, and format churn. Changes to API/configuration/storage contracts require corresponding documentation and relevant behavioral checks in [TESTING.md](TESTING.md).

Review correctness, data contracts, authorization, concurrency, deployment compatibility, and user-visible states before style. Keep deployment and rollback instructions accurate; do not claim a release is published based on a local build.
