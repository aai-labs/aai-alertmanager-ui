import { describe, it, expect } from "vitest";
import {
  incidentQueue,
  overdue,
  selectIncidents,
  type Incident,
} from "./incidents";
const now = Date.parse("2026-10-31T12:00:00Z");
const incident = (extra: Partial<Incident> = {}): Incident => ({
  id: "a",
  namespace: "team-a",
  title: "Storage warning",
  sourceSeverity: "warning",
  urgency: "review_today",
  owner: "",
  firstSeen: "2026-10-20T12:00:00Z",
  lastSeen: "2026-10-31T12:00:00Z",
  version: 1,
  members: [],
  ...extra,
});
describe("incident queues and follow-up", () => {
  it("keeps suppressed critical source incidents urgent even if model says monitor", () => {
    expect(
      incidentQueue(
        incident({
          sourceSeverity: "critical",
          urgency: "monitor",
          members: [
            {
              fingerprint: "a",
              labels: {},
              annotations: {},
              status: { state: "suppressed" },
            },
          ],
        }),
      ),
    ).toBe("now");
  });
  it("cleared incidents still require workflow closure", () => {
    expect(
      incidentQueue(
        incident({
          sourceSeverity: "critical",
          clearedAt: "2026-10-31T10:00:00Z",
        }),
      ),
    ).toBe("unresolved");
  });
  it("does not infer ignored or overdue from age alone", () => {
    expect(overdue(incident(), now)).toBe(false);
    expect(selectIncidents([incident()], "", "overdue", "", now)).toHaveLength(
      0,
    );
    expect(selectIncidents([incident()], "", "week", "", now)).toHaveLength(1);
  });
  it("hides closed work by default and scopes namespace and ownership", () => {
    const all = [
      incident(),
      incident({ id: "b", namespace: "team-b", owner: "Sam" }),
      incident({ id: "c", closedAt: "2026-10-31T10:00:00Z" }),
    ];
    expect(
      selectIncidents(all, "team-a", "unowned", "", now).map((i) => i.id),
    ).toEqual(["a"]);
    expect(
      selectIncidents(all, "", "closed", "", now).map((i) => i.id),
    ).toEqual(["c"]);
  });
  it("sorts oldest first and uses due time independently from acknowledgement", () => {
    const a = incident({ id: "a", firstSeen: "2026-10-29T12:00:00Z" }),
      b = incident({
        id: "b",
        reviewAt: "2026-10-30T12:00:00Z",
        acknowledgedAt: "2026-10-31T11:00:00Z",
      });
    expect(selectIncidents([a, b], "", "all", "", now)[0].id).toBe("b");
    expect(overdue(b, now)).toBe(true);
  });
});
