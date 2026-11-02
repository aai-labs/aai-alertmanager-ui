import { describe, it, expect } from "vitest";
import {
  aggregateTrend,
  aggregateObservedTrend,
  observedTotal,
  podAttentionOrder,
  bytesText,
  controllerNeedsAttention,
  needsAttention,
  ratioText,
  sumKnown,
  type NamespaceResources,
  type Pod,
} from "./cluster";
describe("resource and workload semantics", () => {
  it("does not convert unavailable usage into zero or imply capacity for missing reservations", () => {
    expect(bytesText(null)).toBe("Unknown");
    expect(bytesText(0)).toBe("0 B");
    expect(ratioText(1, 0)).toBe("Unknown");
    expect(sumKnown([1, null])).toBe(null);
    expect(sumKnown([0, 2])).toBe(2);
  });
  it("preserves zero desired replicas as a valid scaled-down controller", () => {
    expect(
      controllerNeedsAttention({
        name: "idle",
        kind: "Deployment",
        desired: 0,
        ready: 0,
        available: 0,
      }),
    ).toBe(false);
    expect(
      controllerNeedsAttention({
        name: "unknown",
        kind: "Deployment",
        desired: 1,
        ready: null,
        available: null,
      }),
    ).toBe(true);
  });
  it("keeps failed and unknown pods actionable while completed jobs are separate", () => {
    const p = { state: "completed" } as Pod;
    expect(needsAttention(p)).toBe(false);
    expect(needsAttention({ ...p, state: "failed" })).toBe(true);
    expect(needsAttention({ ...p, state: "unknown" })).toBe(true);
  });
  it("leaves aggregate chart gaps when any namespace sample is missing", () => {
    const r = (trend: NamespaceResources["trend"]) =>
      ({ trend }) as NamespaceResources;
    const p = {
      at: "2026-11-01T00:00:00Z",
      cpuUsageCores: 1,
      memoryUsageBytes: 1024,
      cpuRequestsCores: 2,
      memoryRequestsBytes: 2048,
    };
    expect(aggregateTrend([r([p]), r([])])[0].cpuUsageCores).toBe(null);
    expect(aggregateTrend([r([p]), r([p])])[0].cpuUsageCores).toBe(2);
  });
  it("labels observed subtotals with coverage instead of losing all data to one missing namespace", () => {
    expect(observedTotal([2, null, 0])).toEqual({
      value: 2,
      reporting: 2,
      total: 3,
    });
    expect(observedTotal([null])).toEqual({
      value: null,
      reporting: 0,
      total: 1,
    });
    const rows = [
      {
        trend: [
          {
            at: "2026-11-01",
            cpuUsageCores: 2,
            memoryUsageBytes: 3,
            cpuRequestsCores: 4,
            memoryRequestsBytes: 5,
          },
        ],
      },
      { trend: [] },
    ] as NamespaceResources[];
    const point = aggregateObservedTrend(rows)[0];
    expect(point.cpuUsageCores).toBe(2);
    expect(point.usageReportingCPU).toBe(1);
    expect(point.namespaceCount).toBe(2);
  });
  it("puts pods needing attention before ready and completed workloads", () => {
    const p = (state: Pod["state"]) => ({ state, name: state }) as Pod;
    expect(
      [p("completed"), p("ready"), p("failed"), p("notReady")]
        .sort(podAttentionOrder)
        .map((p) => p.state),
    ).toEqual(["notReady", "failed", "ready", "completed"]);
  });
});

it("flags controllers whose ready replicas are not yet available", () => {
  expect(
    controllerNeedsAttention({
      name: "web",
      kind: "Deployment",
      desired: 2,
      ready: 2,
      available: 1,
    }),
  ).toBe(true);
  expect(
    controllerNeedsAttention({
      name: "web",
      kind: "Deployment",
      desired: 2,
      ready: 2,
      available: null,
    }),
  ).toBe(true);
});
