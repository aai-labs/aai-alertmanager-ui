# Optional persistence and incident workspace

Status: accepted for the next-version implementation. Recorded: 2026-11-02.

## Decision and reason

The approved implementation plan keeps basic installations as a stateless Alertmanager UI, adds analytics from Prometheus, and enables workflow persistence only when requested. This preserves the easy-install requirement while supporting ownership, review dates, recurrence, and durable follow-up delivery.

Prometheus remains the historical time-series source. Alertmanager retains delivery, silences, and critical paging. SQLite stores local workflow state on a persistent volume with WAL, a writer lock, transactional migrations, and one application replica. Jev evaluation and webhooks are optional server-side integrations.

## Alternatives and consequences

Making a database mandatory would complicate basic installation. Putting workflow state only in the browser would lose durable shared ownership, audit, and delivery state. Copying Prometheus history into SQLite would introduce a second historical source and retention responsibility. The selected design avoids those costs, but enhanced mode requires backup/restore and a single writer; it does not provide horizontally scalable incident storage.

Resource views and deterministic incident grouping target one cluster per instance. Existing multi-cluster monitoring must provide a cluster-filtered source. Missing measurements remain unknown. Model grouping/ranking cannot suppress critical notifications or execute remediation.

See [enhanced mode](../enhanced-mode.md), [resource contracts](../resources-and-workloads.md), and [validation](../enhanced-validation.md). Revisit storage architecture if multi-writer availability or multi-cluster incident management becomes a requirement.
