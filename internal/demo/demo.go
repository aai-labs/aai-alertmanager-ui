// Package demo contains only fictional data for local evaluation and documentation.
package demo

import "time"

func Alerts() []map[string]any {
	now := time.Now().UTC()
	rows := []struct {
		name, ns, severity, resource string
		suppressed                   bool
	}{
		{"CephHealthError", "storage", "critical", "ceph-main", false},
		{"KubeNodeNotReady", "", "critical", "worker-03", false},
		{"KubeDeploymentReplicasMismatch", "storefront", "warning", "checkout", false},
		{"KubePodCrashLooping", "storefront", "warning", "checkout-78d9f", false},
		{"KubeStatefulSetReplicasMismatch", "analytics", "warning", "events-db", false},
		{"PaymentQueueBacklog", "payments", "warning", "payments-worker", false},
		{"KubeJobFailed", "staging", "warning", "nightly-import", false},
		{"KubeJobFailed", "staging", "warning", "catalog-sync", false},
		{"ContainerMemoryNearLimit", "analytics", "warning", "report-worker", false},
		{"InfoInhibitor", "staging", "info", "", true},
		{"DatabaseReplicationLag", "staging", "critical", "test-db", true},
	}
	result := []map[string]any{}
	for i, row := range rows {
		labels := map[string]string{"alertname": row.name, "severity": row.severity}
		if row.ns != "" {
			labels["namespace"] = row.ns
		}
		if row.resource != "" {
			labels["instance"] = row.resource
		}
		state := "active"
		silenced := []string{}
		if row.suppressed {
			state = "suppressed"
			silenced = []string{"11111111-1111-4111-8111-111111111111"}
		}
		result = append(result, map[string]any{"labels": labels, "annotations": map[string]string{"summary": row.name + " on " + row.resource}, "fingerprint": row.name + "-" + row.ns + "-" + row.resource, "startsAt": now.Add(-time.Duration(12+i*9) * time.Minute).Format(time.RFC3339), "endsAt": now.Add(time.Hour).Format(time.RFC3339), "status": map[string]any{"state": state, "silencedBy": silenced, "inhibitedBy": []string{}}, "receivers": []map[string]string{{"name": "on-call"}}})
	}
	return result
}
func Silences() []map[string]any {
	return []map[string]any{{"id": "11111111-1111-4111-8111-111111111111", "matchers": []map[string]any{{"name": "namespace", "value": "staging", "isRegex": false, "isEqual": true}}, "startsAt": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "endsAt": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339), "createdBy": "demo-operator", "comment": "Scheduled staging maintenance", "status": map[string]string{"state": "active"}, "updatedAt": time.Now().UTC().Format(time.RFC3339)}}
}
func Prometheus() map[string]any {
	alerts := []map[string]any{}
	for _, a := range Alerts() {
		alerts = append(alerts, map[string]any{"labels": a["labels"], "annotations": a["annotations"], "state": "firing", "activeAt": a["startsAt"], "value": "1"})
	}
	alerts = append(alerts, map[string]any{"labels": map[string]string{"alertname": "KubePodNotReady", "namespace": "storefront", "severity": "warning", "pod": "catalog-6b84f"}, "annotations": map[string]string{}, "state": "pending", "activeAt": time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339), "value": "1"})
	return map[string]any{"status": "success", "data": map[string]any{"alerts": alerts}}
}
