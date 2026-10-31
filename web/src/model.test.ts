import { describe, it, expect } from "vitest";
import {
  classify,
  normalizeAlerts,
  enrich,
  pendingAlerts,
  searchAlerts,
  groupAlerts,
  safeURL,
  age,
  type Alert,
} from "./model";
const alert = (
  labels: Record<string, string> = {},
  state = "active",
): Alert => ({
  fingerprint: "abc",
  labels: {
    alertname: "MysteryAlert",
    severity: "warning",
    namespace: "team-a",
    ...labels,
  },
  annotations: {},
  startsAt: "2026-01-01T00:00:00Z",
  status: { state },
});
describe("prioritization safety", () => {
  it("never hides unfamiliar or missing-severity alerts", () => {
    expect(classify(alert()).primary).toBe(true);
    expect(classify(alert({ severity: "" })).primary).toBe(true);
  });
  it("promotes critical routine alerts", () =>
    expect(
      classify(alert({ alertname: "KubeJobFailed", severity: "critical" }))
        .category,
    ).toBe("Critical"));
  it("keeps suppressed critical infrastructure prominent", () => {
    expect(
      classify(
        alert(
          { alertname: "CephHealthError", severity: "critical" },
          "suppressed",
        ),
      ).primary,
    ).toBe(true);
    expect(
      classify(alert({ severity: "critical" }, "suppressed")).category,
    ).toBe("Suppressed");
  });
  it("does not infer environment importance from namespace names", () =>
    expect(classify(alert({ namespace: "production" })).category).toBe(
      classify(alert({ namespace: "dev" })).category,
    ));
  it("uses first configured matching rule but cannot hide critical", () => {
    const rules = [
      {
        name: "owner",
        match: { namespace: "team-a" },
        category: "Capacity",
        displayName: "Capacity watch",
      },
    ];
    expect(classify(alert(), rules).displayName).toBe("Capacity watch");
    expect(classify(alert({ severity: "critical" }), rules).category).toBe(
      "Critical",
    );
  });
  it("distinguishes pending from firing", () =>
    expect(
      classify({ ...alert({ severity: "critical" }), pending: true }).category,
    ).toBe("Pending"));
});
describe("data integrity", () => {
  it("deduplicates fingerprints and preserves active state", () =>
    expect(normalizeAlerts([alert({}, "suppressed"), alert()])).toEqual([
      alert(),
    ]));
  it("groups only namespace and category, keeping each alert", () => {
    const groups = groupAlerts([
      classify(alert()),
      classify({ ...alert(), fingerprint: "b" }),
      classify(alert({ namespace: "b" })),
    ]);
    expect(groups).toHaveLength(2);
    expect(groups[1].alerts).toHaveLength(2);
  });
  it("uses labels to enrich only an unambiguous match", () => {
    const p = {
      labels: {
        alertname: "MysteryAlert",
        namespace: "team-a",
        severity: "warning",
      },
      annotations: {},
      state: "firing",
      value: "1",
      activeAt: "",
    };
    expect(enrich([alert({ cluster: "external" })], [p])[0].value).toBe("1");
    expect(enrich([alert()], [p, p])[0].value).toBeUndefined();
  });
  it("retains pending labels and namespace", () =>
    expect(
      pendingAlerts([
        {
          labels: { namespace: "team-a" },
          annotations: {},
          state: "pending",
          value: "0",
          activeAt: "",
        },
      ])[0].labels.namespace,
    ).toBe("team-a"));
  it("combines namespace with label search without cross-namespace leakage", () => {
    const list = [classify(alert()), classify(alert({ namespace: "team-b" }))];
    expect(searchAlerts(list, "team-a", "severity=warning")).toHaveLength(1);
    expect(searchAlerts(list, "team-a", "team-b")).toHaveLength(0);
  });
  it("rejects executable links and handles invalid ages", () => {
    expect(safeURL("javascript:alert(1)")).toBeUndefined();
    expect(safeURL("https://example.org/runbook")).toBeTruthy();
    expect(age("bad")).toBe("Unknown");
  });
});

it("keeps known heartbeat rules quiet unless explicitly critical", () => {
  expect(
    classify(alert({ alertname: "Watchdog", severity: "none" })).category,
  ).toBe("Informational");
  expect(
    classify(alert({ alertname: "Watchdog", severity: "critical" })).category,
  ).toBe("Critical");
});
