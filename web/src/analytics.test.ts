import { describe, it, expect } from "vitest";
import {
  dayDescription,
  heatLevel,
  percent,
  type AnalyticsDay,
} from "./analytics";
const day = (extra: Partial<AnalyticsDay> = {}): AnalyticsDay => ({
  date: "2026-10-31",
  knownSeconds: 86400,
  expectedSeconds: 86400,
  firingSeconds: 3600,
  firingPercent: 100 / 24,
  episodes: 4,
  ...extra,
});
describe("analytics presentation", () => {
  it("never presents absent coverage as no firing", () => {
    expect(heatLevel(day({ knownSeconds: 0, firingPercent: null }))).toBe(
      "unknown",
    );
    expect(percent(null)).toBe("Unknown");
  });
  it("distinguishes measured zero from unknown", () => {
    expect(heatLevel(day({ firingPercent: 0, firingSeconds: 0 }))).toBe("none");
    expect(percent(0)).toBe("0.0%");
  });
  it("describes partial-day coverage and episode counts accessibly", () => {
    expect(
      dayDescription(day({ knownSeconds: 3600, expectedSeconds: 7200 })),
    ).toContain("1.0 of 2.0 hours covered");
    expect(dayDescription(day())).toContain("4 observed new episodes");
  });
});
