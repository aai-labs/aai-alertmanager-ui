export type Labels = Record<string, string>;
export type Alert = {
  fingerprint: string;
  labels: Labels;
  annotations: Labels;
  startsAt: string;
  endsAt?: string;
  status: { state: string; silencedBy?: string[]; inhibitedBy?: string[] };
  pending?: boolean;
  value?: string;
};
export type PromAlert = {
  labels: Labels;
  annotations: Labels;
  state: string;
  activeAt: string;
  value: string;
};
export type Priority = {
  name: string;
  match: Labels;
  category: string;
  displayName?: string;
};
export type Context = {
  readOnly: boolean;
  tenantMode: boolean;
  identity: string;
  namespaces: string[] | null;
  prometheusEnabled: boolean;
  demo: boolean;
  priorities: Priority[];
};
export type Matcher = {
  name: string;
  value: string;
  isRegex: boolean;
  isEqual: boolean;
};
export type Silence = {
  id?: string;
  matchers: Matcher[];
  startsAt: string;
  endsAt: string;
  createdBy: string;
  comment: string;
  status?: { state: string };
};
export type Classified = Alert & {
  category: string;
  reason: string;
  displayName: string;
  primary: boolean;
};
const core = new Set([
  "CephHealthError",
  "CephHealthWarning",
  "CephOSDDown",
  "CephOSDNearFull",
  "CephOSDFull",
  "CephMonQuorumAtRisk",
  "CephPGUnavailable",
  "CephPGsInactive",
  "KubeNodeNotReady",
  "KubeAPIDown",
  "KubeControllerManagerDown",
  "KubeSchedulerDown",
  "etcdInsufficientMembers",
  "etcdNoLeader",
]);
const availability = new Set([
  "KubePodCrashLooping",
  "KubePodNotReady",
  "KubeDeploymentReplicasMismatch",
  "KubeStatefulSetReplicasMismatch",
  "KubeDaemonSetRolloutStuck",
]);
export const secondaryCategories = [
  "Job failures",
  "Capacity",
  "Informational",
  "Suppressed",
  "Pending",
];
export function classify(a: Alert, rules: Priority[] = []): Classified {
  const custom = rules.find((r) =>
    Object.entries(r.match).every(([k, v]) => a.labels[k] === v),
  );
  let category =
    custom?.category ||
    (a.labels.alert_category === "core-cluster-health" ||
    core.has(a.labels.alertname)
      ? "Core infrastructure"
      : availability.has(a.labels.alertname)
        ? "Availability"
        : a.labels.alertname === "KubeJobFailed"
          ? "Job failures"
          : [
                "ContainerMemoryNearLimit",
                "KubePersistentVolumeFillingUp",
                "KubeQuotaAlmostFull",
              ].includes(a.labels.alertname)
            ? "Capacity"
            : ["Watchdog", "InfoInhibitor"].includes(a.labels.alertname) ||
                a.labels.severity === "info"
              ? "Informational"
              : "Other active alerts");
  const reason = custom
    ? `Rule: ${custom.name}`
    : a.labels.alert_category === "core-cluster-health"
      ? "Label: alert_category"
      : category === "Other active alerts"
        ? "Default: unfamiliar alert"
        : category === "Informational" &&
            !["Watchdog", "InfoInhibitor"].includes(a.labels.alertname)
          ? "Label: severity=info"
          : `Preset: ${a.labels.alertname}`;
  if (a.pending) category = "Pending";
  else if (
    a.status.state === "suppressed" &&
    !(category === "Core infrastructure" && a.labels.severity === "critical")
  )
    category = "Suppressed";
  else if (
    a.labels.severity === "critical" &&
    category !== "Core infrastructure"
  )
    category = "Critical";
  return {
    ...a,
    category,
    reason,
    displayName: custom?.displayName || a.labels.alertname || "Unnamed alert",
    primary: !secondaryCategories.includes(category),
  };
}
export function normalizeAlerts(alerts: Alert[]): Alert[] {
  const unique = new Map<string, Alert>();
  for (const a of alerts) {
    const key =
      a.fingerprint || JSON.stringify(Object.entries(a.labels).sort());
    const previous = unique.get(key);
    if (!previous || a.status.state === "active")
      unique.set(key, {
        ...a,
        fingerprint: key,
        annotations: a.annotations || {},
      });
  }
  return [...unique.values()];
}
export function enrich(alerts: Alert[], prom: PromAlert[]): Alert[] {
  return alerts.map((a) => {
    const candidates = prom.filter(
      (p) =>
        p.state === "firing" &&
        Object.entries(p.labels).every(([k, v]) => a.labels[k] === v),
    );
    return candidates.length === 1 ? { ...a, value: candidates[0].value } : a;
  });
}
export function pendingAlerts(prom: PromAlert[]): Alert[] {
  return prom
    .filter((a) => a.state === "pending")
    .map((a) => ({
      fingerprint: "pending:" + JSON.stringify(Object.entries(a.labels).sort()),
      labels: a.labels,
      annotations: a.annotations || {},
      startsAt: a.activeAt,
      status: { state: "pending" },
      pending: true,
      value: a.value,
    }));
}
export function searchAlerts(
  alerts: Classified[],
  namespace: string,
  query: string,
) {
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  return alerts.filter(
    (a) =>
      (!namespace ||
        (namespace === "__cluster__"
          ? !a.labels.namespace
          : a.labels.namespace === namespace)) &&
      terms.every(
        (term) =>
          Object.entries(a.labels).some(([k, v]) =>
            `${k}=${v}`.toLowerCase().includes(term),
          ) || a.displayName.toLowerCase().includes(term),
      ),
  );
}
export type Group = {
  key: string;
  namespace: string;
  category: string;
  alerts: Classified[];
  critical: boolean;
};
export function groupAlerts(alerts: Classified[]): Group[] {
  const groups = new Map<string, Group>();
  for (const a of alerts) {
    const key = JSON.stringify([a.labels.namespace || "", a.category]);
    let g = groups.get(key);
    if (!g) {
      g = {
        key,
        namespace: a.labels.namespace || "Cluster-wide",
        category: a.category,
        alerts: [],
        critical: false,
      };
      groups.set(key, g);
    }
    g.alerts.push(a);
    g.critical ||= a.labels.severity === "critical";
  }
  return [...groups.values()].sort(
    (a, b) =>
      Number(b.critical) - Number(a.critical) ||
      Number(b.category === "Core infrastructure") -
        Number(a.category === "Core infrastructure") ||
      Number(b.category === "Availability") -
        Number(a.category === "Availability") ||
      a.namespace.localeCompare(b.namespace) ||
      a.category.localeCompare(b.category),
  );
}
export function age(date: string, now = Date.now()) {
  const ms = now - Date.parse(date);
  if (!Number.isFinite(ms)) return "Unknown";
  if (ms < 0) return "Not started";
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return "<1m";
  if (mins < 60) return `${mins}m`;
  if (mins < 1440) return `${Math.floor(mins / 60)}h ${mins % 60}m`;
  return `${Math.floor(mins / 1440)}d ${Math.floor((mins % 1440) / 60)}h`;
}
export function safeURL(value?: string) {
  if (!value) return undefined;
  try {
    const u = new URL(value);
    return ["http:", "https:"].includes(u.protocol) ? u.href : undefined;
  } catch {
    return undefined;
  }
}
export function resource(a: Alert) {
  return (
    a.labels.pod ||
    a.labels.deployment ||
    a.labels.statefulset ||
    a.labels.job_name ||
    a.labels.node ||
    a.labels.instance ||
    "—"
  );
}
export function alertState(a: Alert) {
  if (a.pending) return "Pending";
  if (a.status.silencedBy?.length) return "Silenced";
  if (a.status.inhibitedBy?.length) return "Inhibited";
  return a.status.state === "suppressed"
    ? "Suppressed"
    : a.status.state === "active"
      ? "Active"
      : a.status.state;
}
