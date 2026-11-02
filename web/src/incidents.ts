import type { Labels } from "./model";
export type IncidentMember = {
  fingerprint: string;
  labels: Labels;
  annotations: Labels;
  status: { state?: string; silencedBy?: string[]; inhibitedBy?: string[] };
};
export type IncidentEvent = {
  id?: number | string;
  at: string;
  actor: string;
  kind: string;
  detail?: string;
};
export type Incident = {
  id: string;
  namespace: string;
  title: string;
  sourceSeverity: string;
  urgency: string;
  owner: string;
  acknowledgedAt?: string;
  reviewAt?: string;
  firstSeen: string;
  lastSeen: string;
  clearedAt?: string;
  closedAt?: string;
  previousId?: string;
  version: number;
  members: IncidentMember[];
  timeline?: IncidentEvent[];
  model?: {
    model: string;
    policyVersion: string;
    createdAt: string;
    applied: boolean;
    team?: string;
    runbook?: string;
    decisions: unknown;
  };
};
export type IncidentResponse = {
  incidents: Incident[];
  source: { state: string; lastSuccess?: string };
  readOnly: boolean;
  total?: number;
  limit?: number;
  offset?: number;
};
export function incidentQueue(
  i: Incident,
): "now" | "unresolved" | "monitoring" {
  if (i.closedAt) return "monitoring";
  if (
    !i.clearedAt &&
    (i.sourceSeverity === "critical" ||
      ["immediate", "act_now", "immediate_review"].includes(i.urgency))
  )
    return "now";
  return i.urgency === "monitor" && !i.clearedAt ? "monitoring" : "unresolved";
}
export function overdue(i: Incident, now = Date.now()) {
  return (
    !i.closedAt &&
    !!i.reviewAt &&
    Number.isFinite(Date.parse(i.reviewAt)) &&
    Date.parse(i.reviewAt) < now
  );
}
export function selectIncidents(
  items: Incident[],
  namespace: string,
  filter: string,
  search: string,
  now = Date.now(),
) {
  return items
    .filter(
      (i) =>
        (!namespace || i.namespace === namespace) &&
        (filter === "closed" ? !!i.closedAt : !i.closedAt) &&
        (filter !== "unowned" || !i.owner) &&
        (filter !== "overdue" || overdue(i, now)) &&
        (filter !== "week" || now - Date.parse(i.firstSeen) >= 7 * 86400000) &&
        `${i.title} ${i.namespace} ${i.owner} ${i.members.map((m) => m.labels.alertname).join(" ")}`
          .toLowerCase()
          .includes(search.toLowerCase()),
    )
    .sort(
      (a, b) =>
        Date.parse(a.firstSeen) - Date.parse(b.firstSeen) ||
        a.id.localeCompare(b.id),
    );
}
export function utcDate(value?: string) {
  if (!value || !Number.isFinite(Date.parse(value))) return "—";
  return new Date(value).toISOString().replace("T", " ").slice(0, 16) + " UTC";
}
export function hoursOpen(value: string, now = Date.now()) {
  const days = Math.max(0, (now - Date.parse(value)) / 86400000);
  return days >= 1 ? `${Math.floor(days)}d` : `${Math.floor(days * 24)}h`;
}
