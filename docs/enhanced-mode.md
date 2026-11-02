# Analytics and the incident workspace

The default application remains a stateless Alertmanager UI. Prometheus enables historical analytics. Enhanced mode adds a local SQLite workflow database; Jev and follow-up webhooks are separately opt-in. None replaces Alertmanager notification routing.

## Namespace analytics

Connect `PROMETHEUS_URL` and open **Analytics**. Seven- and thirty-day windows use UTC calendar days, including the current partial day, at one-minute resolution.

- **Time with alerts** is the union of intervals with one or more firing alerts in a namespace, not their summed durations.
- **Episodes** are observed starts of a distinct firing label set. An alert present at the window boundary is not counted as a new episode. Unknown intervals do not establish a new episode.
- **Days with alerts** count days with observed firing time. Informational signals and heartbeat rules are excluded by default; the severity filter can include them. Suppression does not remove a firing condition from history.
- Percentages use confirmed source coverage as their denominator. Unknown coverage is never shown as healthy. This measures alert activity, not uptime or a stability score. Monitoring rules, label churn, and scrape/evaluation intervals affect comparisons.

The default source coverage expression is `up and on (job, instance) prometheus_build_info`. This identifies self-monitoring targets without a Helm release or job-name assumption. If self-monitoring metrics are absent, coverage remains unknown. Override `analytics.coverageQuery` for another coverage signal; it must return a positive value while the Prometheus source is observed. The server reduces it to one coverage series. This does not prove every tenant service was monitored. No namespace inventory is inferred: operator comparisons show namespaces observed in the requested history; tenant grants and an explicitly requested namespace can also show zero-alert rows.

Daily chunks use fixed server-side queries, a 20-second total deadline, at most 10,000 series per response, two million samples per request, and 512 observed namespaces. Two concurrent historical requests are allowed. Successful complete results are cached for five minutes by authorization scope and filters. Gaps, retention limits, upstream warnings, or query limits produce explicitly incomplete results. Short episodes between samples may be missed. A changed upstream label set is a different alert identity.

## Enable persistent incidents

```yaml
alertmanager:
  url: http://alertmanager.monitoring:9093
prometheus:
  url: http://prometheus.monitoring:9090
analytics:
  coverageQuery: 'up and on (job, instance) prometheus_build_info'
enhanced:
  enabled: true
  databasePath: /data/alerts.db
  retentionDays: 90
```

Alternatively use `ENHANCED=true DATABASE_PATH=/data/alerts.db`. Mount a writable persistent volume at `/data`, owned by the container UID/GID 65532. The application retains its read-only root filesystem. One replica is required. Use a local/block-backed filesystem with working POSIX locks, not a shared database file across hosts.

The Helm chart supports `enhanced.enabled=true`, `enhanced.persistence.size` (default `1Gi`), `storageClass`, and `existingClaim`. It uses a Recreate deployment strategy and retains the generated claim on uninstall. When enabling via a configuration Secret, also enable the Helm enhanced setting so deployment strategy and volume setup match.

Use one cluster per enhanced instance; namespace/alert-name grouping assumes that cluster boundary. The collector polls Alertmanager every 30 seconds. It deduplicates receiver copies by fingerprint, initially groups by namespace and alert name, and records changes rather than unchanged snapshots. Incidents are first observed locally: history does not invent pre-installation ownership or responses. The `lastSeen` field is the last persisted change/observation, not a heartbeat; source freshness is reported separately.

Two consecutive successful snapshots without members mark a condition cleared; a failed fetch resets the missing streak. Closure is separate and is only permitted after clearance. Returning conditions within 24 hours reopen the same incident; later recurrence starts a linked incident. Closed records and associated audit history are retained for 90 days by default; open incidents are retained. Operators can assign an owner, acknowledge, schedule a review, and move membership within a namespace. All edits use optimistic version checks. A shared login is recorded as `operator`, not an invented personal identity. Tenants remain read-only.

## Optional Jev evaluation

```yaml
triage:
  enabled: true
  apply: false # Start in shadow evaluation mode; set true after reviewing outcomes.
  apiKeyFile: /etc/alerts-secrets/openrouter-key
  model: typesafe/jev-1.13
  monthlyBudgetUSD: 10
  policyVersion: "1"
  policy: "Production storage unavailability requires immediate investigation. Unknown impact requires review."
  teams:
    - id: platform
      description: "Owns cluster nodes, storage, and networking."
  runbooks:
    - id: storage-review
      description: "Approved Ceph availability investigation for storage incidents."
```

Jev uses the OpenRouter Decisions API, not chat completions. Enable it explicitly to permit external transmission of selected alert data. Only `alertname`, `namespace`, `severity`, `service`, `job`, `workload`, `team`, `environment`, and `alert_category` labels, plus bounded `summary` and `description` annotations, are included. Other labels, generator URLs, silence details, and upstream credentials are excluded. Selected annotation text can still contain sensitive operational information: review your alert rules before enabling external evaluation.

The worker batches every minute and evaluates at most five changed contexts per cycle; larger backlogs wait for another cycle. Requests contain at most twenty candidates and 64KB of encoded input. Questions select from configured options, treating annotation text as untrusted evidence. Inputs are hashed with model and policy version; successful results are reused across restarts. Exact request data is not persisted in model logs. Same-namespace membership may be applied; cross-namespace relationships remain hypotheses visible only to operators. Operator membership corrections remain authoritative. Critical source severity always retains immediate urgency.

`apply: false` records decisions and usage without changing grouping or ranking. `apply: true` applies bounded decisions with confidence and selected-option probability of at least 0.8. These are conservative gates, not measured accuracy guarantees. Insufficient evidence leaves deterministic behavior in place. Jev never writes silences, changes routes, runs commands, or suppresses critical paging. Team/runbook selections are recommendations, not automatic ownership claims or executable commands.

Budget reservations are persisted before each request using a conservative token estimate and price allowance. Ambiguous failed calls keep the reservation charged; successful responses settle reported usage. The app does not blindly retry network requests: an unchanged failed context gets at most three attempts, one per later cycle, persisted across restarts. After the retry limit, change the policy version to request re-evaluation. Budget exhaustion disables evaluation, not alert browsing or polling. Actual provider price changes can exceed an estimate; set an account-level provider spending limit too. Provider outages, context limits, usage, and the latest hundred shadow decisions are visible to operators. Read-only mode permits collection and explicitly enabled shadow evaluation but prevents model application and follow-up delivery. No live-provider accuracy claim is made by the automated fixture tests.

## Optional due-review webhook

```yaml
followUp:
  url: https://hooks.example.com/alerts/review
  bearerTokenFile: /etc/alerts-secrets/webhook-token
  publicURL: https://alerts.example.com/alerts/
```

Both URLs require HTTPS. The bearer token is loaded from a mounted secret. Every event contains `eventId`, `type: incident.review_due`, `incidentId`, `owner`, `urgency`, `dueAt`, and `link`. `Idempotency-Key` carries the same stable event ID. The receiver must deduplicate events and decide how to contact the person/team.

The outbox persists one event per review-date revision, delivers at least once, and retries with exponential backoff (one minute up to one hour) for up to 24 hours. Failed delivery remains visible for operator retry. HTTP 2xx is labeled **webhook accepted**, not person notified. Redirects are not followed. Acknowledgement does not cancel a scheduled review; changing the date or closing cancels obsolete queued work. An already in-flight HTTP request cannot be recalled. Review delivery is independent of Alertmanager critical paging.

## API additions

All routes respect `BASE_PATH`, authentication, and namespace grants:

- `GET /api/analytics?days=7|30&namespace=...&severity=actionable|all|critical|warning|info` returns UTC daily data, source coverage, resolution, warnings, and incomplete status. Arbitrary query expressions are not accepted.
- `GET /api/incidents?limit=100&offset=0` returns scoped summaries, authorized total, and source freshness (maximum page size 200). Urgent active incidents sort first, then oldest open work, then closed history. The UI explicitly loads more; its filters/counts apply to loaded records. `GET /api/incidents/{id}` returns full evidence and timeline.
- `PATCH /api/incidents/{id}` accepts `version` and optional `owner`, `acknowledge`, `reviewAt` (RFC3339; empty clears), or `close`. Stale versions return 409.
- `PATCH /api/incidents/{id}/membership` accepts `version`, `targetVersion`, `targetIncidentId`, and `fingerprint` for an operator correction.
- `GET /api/incidents/{id}/deliveries`, `POST /api/deliveries/{id}/retry`, and `GET /api/triage/status` are operator-only.

Workflow writes require JSON and same-origin requests and obey read-only mode. Tenant responses omit model decisions, operator timelines, suppression IDs, and cross-namespace relationships. Historical queries constrain namespaces before retrieving alerts; caches are partitioned by grants. Namespace-less alerts are operator-only.

## Backup, restore, and rollout

1. Stop the single enhanced-mode process cleanly before taking a filesystem backup. Preserve the entire data directory, including any SQLite WAL/SHM files. Never copy just an active database file.
2. Restore the complete directory while the application is stopped, preserving ownership and permissions. The lock file may exist; the OS lock is released when the old process exits. Do not delete a lock to bypass a running writer.
3. Start the same or a compatible newer application version. Startup applies transactional schema initialization and refuses an unsupported schema. Test restores before upgrades; keep the old image and backup for rollback.
4. Enable analytics against a read-only source first. Enable enhanced mode on a separate protected route with `readOnly: true` to inspect collection without workflow mutations. Start Jev with `apply: false` and review disagreements before enabling application. Configure a test webhook receiver before connecting real recipients.

Database locking is tested within one host and is not a substitute for a single-replica deployment. Production cluster deployment remains an operator action. Existing Alertmanager routes and upstream APIs need no changes.

Provider contract: [OpenRouter Decisions API](https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-request). The adapter validates structured choices and usage; it does not use chat completions.

Architecture rationale: [Optional persistence and incident workspace](adr/2026-11-02-optional-incident-workspace.md).
