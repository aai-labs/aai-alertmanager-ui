# Prioritization

Every alert is categorized deterministically in `web/src/model.ts`. No AI service, generated diagnosis, or root-cause correlation is used.

1. Apply the first matching optional priority rule (exact label values, AND semantics).
2. Otherwise apply the bundled label/name presets below; unknown alerts default to **Other active alerts**.
3. Pending rules always go to **Pending**.
4. Suppressed alerts go to **Suppressed**, except critical **Core infrastructure** alerts remain prominent and explicitly suppressed.
5. Active alerts with `severity=critical` are prominent, regardless of a routine category.

## Bundled presets

| Category | Match |
| --- | --- |
| Core infrastructure | `alert_category=core-cluster-health` |
| Core infrastructure | `CephHealthError`, `CephHealthWarning`, `CephOSDDown`, `CephOSDNearFull`, `CephOSDFull`, `CephMonQuorumAtRisk`, `CephPGUnavailable`, `CephPGsInactive` |
| Core infrastructure | `KubeNodeNotReady`, `KubeAPIDown`, `KubeControllerManagerDown`, `KubeSchedulerDown`, `etcdInsufficientMembers`, `etcdNoLeader` |
| Availability | `KubePodCrashLooping`, `KubePodNotReady`, `KubeDeploymentReplicasMismatch`, `KubeStatefulSetReplicasMismatch`, `KubeDaemonSetRolloutStuck` |
| Job failures | `KubeJobFailed` |
| Capacity | `ContainerMemoryNearLimit`, `KubePersistentVolumeFillingUp`, `KubeQuotaAlmostFull` |
| Informational | `Watchdog`, `InfoInhibitor`, or `severity=info`, unless an earlier name/category preset matched |
| Other active alerts | Everything else, including absent/unrecognized severity |

These are exact, case-sensitive names, not fuzzy matches. A cluster need not run Kubernetes or Ceph: unmatched alerts remain in the main queue. Presets describe presentation priority, not validated infrastructure health. A warning-level failed job can still be important to your team; promote its labels using an override.

## Groups and counts

Cards group by namespace and category. They preserve each alert instance and deduplicate Alertmanager receiver copies using the alert fingerprint. They do not establish that grouped alerts share a root cause. Counts are alert instances, not failed executions, affected users, or incidents.

Critical groups sort first, then core infrastructure, availability, and other categories. Namespace is the tie-breaker. Filters apply to counts and cards; view tabs show totals before filters. Alert age uses Alertmanager `startsAt`; pending duration uses Prometheus `activeAt`.

Rule values are displayed without guessing a unit. Prometheus enrichment requires exactly one firing alert whose labels match the Alertmanager alert; additional Alertmanager external labels are allowed. Ambiguous matches receive no value.

## Tune one alert family

```yaml
priorities:
  - name: Production imports
    match:
      alertname: KubeJobFailed
      namespace: data-production
    category: Availability
    displayName: Import job failed
```

This moves those warning-level job failures into the main queue, keeping the original source labels. It does not change alert evaluation, routing, inhibition, or notifications. See [configuration](configuration.md) for supported categories and tenant metadata visibility.
