export type AnalyticsDay = {
  date: string;
  knownSeconds: number;
  expectedSeconds: number;
  firingSeconds: number;
  firingPercent: number | null;
  episodes: number;
};
export type NamespaceAnalytics = {
  namespace: string;
  knownSeconds: number;
  firingSeconds: number;
  firingPercent: number | null;
  episodes: number;
  daysWithAlerts: number;
  days: AnalyticsDay[];
};
export type AnalyticsResponse = {
  windowDays: number;
  start: string;
  end: string;
  generatedAt: string;
  timeZone: string;
  resolutionSeconds: number;
  incomplete: boolean;
  warnings: string[];
  coverage: {
    knownSeconds: number;
    expectedSeconds: number;
    percent: number | null;
    kind: string;
  };
  namespaces: NamespaceAnalytics[];
};
export function percent(value: number | null | undefined) {
  return value == null ? "Unknown" : `${value.toFixed(1)}%`;
}
export function dayDescription(d: AnalyticsDay) {
  return `${d.date.slice(0, 10)} UTC: ${(d.firingSeconds / 3600).toFixed(1)} hours with firing alerts; ${d.episodes} observed new episodes; ${(d.knownSeconds / 3600).toFixed(1)} of ${(d.expectedSeconds / 3600).toFixed(1)} hours covered`;
}
export function heatLevel(day: AnalyticsDay) {
  if (!day.knownSeconds) return "unknown";
  if (day.firingPercent === null) return "unknown";
  if (day.firingPercent === 0) return "none";
  if (day.firingPercent < 25) return "low";
  if (day.firingPercent < 75) return "medium";
  return "high";
}
