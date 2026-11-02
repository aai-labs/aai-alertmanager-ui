# Changelog

## Unreleased

### Added

- Namespace alert analytics with UTC daily heatmaps, recurrence, and explicit source-coverage gaps.
- Optional persistent incident workspace with ownership, review dates, audit history, recurrence, and namespace grouping.
- Optional budgeted Jev triage and durable due-review webhooks; existing Alertmanager paging remains independent.
- Cluster overview, resource trends/quotas/storage, and pod/controller/service observations using existing Prometheus metrics.
- Enhanced-mode Helm persistence, backup/restore guidance, synthetic screenshots, and lifecycle/security/browser regression checks.
- Installed and bootstrapped ai-starter guidance, tailored to Go/React, this repository's commands, and read-only cluster validation.

### Changed

- Centralized review-delivery eligibility in the incident store and separated the typed webhook transport from workflow orchestration.

- Clear loading, empty, failure, and partial-data states; dated heatmap columns and local quota filters.
- Portable source-coverage detection, bounded collector caches, and conservative interpretation of probes, endpoints, and missing limits.

### Fixed

- Tenant incident projections no longer mutate shared member data.
- Late incident responses cannot replace a newer selection or reopen a closed detail dialog; detail failures are separate from list freshness.

Released artifacts remain documented in README; these entries do not imply a published image or production rollout.
