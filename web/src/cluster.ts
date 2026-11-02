export type ResourcePoint = {
  at: string;
  cpuUsageCores: number | null;
  memoryUsageBytes: number | null;
  cpuRequestsCores: number | null;
  memoryRequestsBytes: number | null;
};
export type NamespaceResources = {
  namespace: string;
  cpuUsageCores: number | null;
  memoryUsageBytes: number | null;
  cpuRequestsCores: number | null;
  memoryRequestsBytes: number | null;
  cpuLimitsCores: number | null;
  memoryLimitsBytes: number | null;
  pvcRequestedBytes: number | null;
  pvcCapacityBytes: number | null;
  pvcUsedBytes: number | null;
  unlimitedCPUContainers: number | null;
  unlimitedMemoryContainers: number | null;
  trend: ResourcePoint[];
  quotas?: Array<{
    name: string;
    resource: string;
    hard: number | null;
    used: number | null;
  }>;
};
export type NodeResources = {
  node: string;
  cpuUsageCores: number | null;
  cpuAllocatableCores: number | null;
  memoryUsageBytes: number | null;
  memoryAllocatableBytes: number | null;
};
export type ResourcesResponse = {
  generatedAt: string;
  hours: number;
  incomplete: boolean;
  warnings: string[];
  notes?: string[];
  resolutionSeconds: number;
  cluster: null | {
    cpuAllocatableCores: number | null;
    memoryAllocatableBytes: number | null;
    cpuUsageCores: number | null;
    memoryUsageBytes: number | null;
  };
  namespaces: NamespaceResources[];
  nodes?: NodeResources[];
};
export type PodState =
  "ready" | "notReady" | "unscheduled" | "completed" | "failed" | "unknown";
export type Pod = {
  name: string;
  uid?: string;
  node?: string;
  ownerKind?: string;
  ownerName?: string;
  phase: string;
  state: PodState;
  waitingReasons: string[];
  cpuCores: number | null;
  memoryBytes: number | null;
  observedAt?: string;
};
export type Controller = {
  kind: string;
  name: string;
  desired: number | null;
  ready: number | null;
  available: number | null;
  observedAt?: string;
};
export type Service = {
  name: string;
  type?: string;
  clusterIP?: string;
  endpointsReady: number | null;
  reachability: "unknown" | "reachable" | "failed" | "degraded";
  probeTargets?: number;
  failedProbeTargets?: number;
  unknownProbeTargets?: number;
  observedAt?: string;
};
export type NamespaceWorkloads = {
  namespace: string;
  pods: Pod[];
  controllers: Controller[];
  services: Service[];
  counts: Record<PodState, number>;
};
export type WorkloadsResponse = {
  generatedAt: string;
  incomplete: boolean;
  warnings: string[];
  source: { state: string; oldestSampleAt?: string; maxAgeSeconds: number };
  namespaces: NamespaceWorkloads[];
};
export const podStates: Record<PodState, string> = {
  ready: "Ready",
  notReady: "Not ready",
  unscheduled: "Not scheduled",
  completed: "Completed",
  failed: "Failed",
  unknown: "Unknown",
};
export function known(value: number | null | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value);
}
export function numberText(value: number | null | undefined, digits = 2) {
  return known(value)
    ? value.toLocaleString(undefined, { maximumFractionDigits: digits })
    : "Unknown";
}
export function bytesText(value: number | null | undefined) {
  if (!known(value)) return "Unknown";
  if (value === 0) return "0 B";
  const power = Math.min(
    4,
    Math.max(0, Math.floor(Math.log(Math.abs(value)) / Math.log(1024))),
  );
  return `${numberText(value / 1024 ** power, 1)} ${["B", "KiB", "MiB", "GiB", "TiB"][power]}`;
}
export function ratioText(
  value: number | null | undefined,
  capacity: number | null | undefined,
) {
  return known(value) && known(capacity) && capacity > 0
    ? `${numberText((value / capacity) * 100, 1)}%`
    : "Unknown";
}
export function sumKnown(values: (number | null | undefined)[]): number | null {
  return values.length && values.every(known)
    ? values.reduce<number>((sum, v) => sum + (v as number), 0)
    : null;
}
export function needsAttention(p: Pod) {
  return ["notReady", "unscheduled", "failed", "unknown"].includes(p.state);
}
export function controllerNeedsAttention(c: Controller) {
  return (
    !known(c.desired) ||
    !known(c.ready) ||
    !known(c.available) ||
    c.ready < c.desired ||
    c.available < c.desired
  );
}
export function aggregateTrend(rows: NamespaceResources[]): ResourcePoint[] {
  const times = [
    ...new Set(rows.flatMap((r) => r.trend.map((p) => p.at))),
  ].sort();
  return times.map((at) => {
    const samples = rows.map((r) => r.trend.find((p) => p.at === at));
    return {
      at,
      cpuUsageCores: sumKnown(samples.map((p) => p?.cpuUsageCores)),
      memoryUsageBytes: sumKnown(samples.map((p) => p?.memoryUsageBytes)),
      cpuRequestsCores: sumKnown(samples.map((p) => p?.cpuRequestsCores)),
      memoryRequestsBytes: sumKnown(samples.map((p) => p?.memoryRequestsBytes)),
    };
  });
}
export function workspaceLink(view: string, namespace: string) {
  const query = new URLSearchParams({ view });
  if (namespace) query.set("namespace", namespace);
  return `?${query}`;
}

export function quotaValue(resource: string, value: number | null) {
  if (/(^|[./])(memory|storage)$/.test(resource)) return bytesText(value);
  if (/(^|[.])cpu$/.test(resource)) return numberText(value) + " cores";
  return numberText(value, 0);
}

export function observedTotal(values: (number | null | undefined)[]) {
  const measured = values.filter(known);
  return {
    value: measured.length ? measured.reduce((sum, v) => sum + v, 0) : null,
    reporting: measured.length,
    total: values.length,
  };
}
export type ObservedResourcePoint = ResourcePoint & {
  usageReportingCPU: number;
  usageReportingMemory: number;
  requestsReportingCPU: number;
  requestsReportingMemory: number;
  namespaceCount: number;
};
export function aggregateObservedTrend(
  rows: NamespaceResources[],
): ObservedResourcePoint[] {
  const maps = rows.map((r) => new Map(r.trend.map((p) => [p.at, p])));
  const times = [
    ...new Set(rows.flatMap((r) => r.trend.map((p) => p.at))),
  ].sort();
  return times.map((at) => {
    const samples = maps.map((m) => m.get(at)),
      cpu = observedTotal(samples.map((p) => p?.cpuUsageCores)),
      memory = observedTotal(samples.map((p) => p?.memoryUsageBytes)),
      cpuRequests = observedTotal(samples.map((p) => p?.cpuRequestsCores)),
      memoryRequests = observedTotal(
        samples.map((p) => p?.memoryRequestsBytes),
      );
    return {
      at,
      cpuUsageCores: cpu.value,
      memoryUsageBytes: memory.value,
      cpuRequestsCores: cpuRequests.value,
      memoryRequestsBytes: memoryRequests.value,
      usageReportingCPU: cpu.reporting,
      usageReportingMemory: memory.reporting,
      requestsReportingCPU: cpuRequests.reporting,
      requestsReportingMemory: memoryRequests.reporting,
      namespaceCount: rows.length,
    };
  });
}
export function podAttentionOrder(a: Pod, b: Pod) {
  const order: Record<PodState, number> = {
    notReady: 0,
    unscheduled: 1,
    failed: 2,
    unknown: 3,
    ready: 4,
    completed: 5,
  };
  return order[a.state] - order[b.state] || a.name.localeCompare(b.name);
}
