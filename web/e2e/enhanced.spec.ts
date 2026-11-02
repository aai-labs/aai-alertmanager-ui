import { test, expect } from "@playwright/test";
const context = {
  readOnly: false,
  tenantMode: false,
  identity: "test-operator",
  namespaces: null,
  prometheusEnabled: false,
  demo: false,
  priorities: [],
  analyticsEnabled: true,
  incidentsEnabled: true,
  triageEnabled: false,
  followUpEnabled: false,
};
const incident = {
  id: "case-1",
  namespace: "payments",
  title: "Persistent checkout restarts",
  sourceSeverity: "warning",
  urgency: "today",
  owner: "",
  firstSeen: "2026-01-01T00:00:00Z",
  lastSeen: "2026-10-31T12:00:00Z",
  reviewAt: "2026-01-02T00:00:00Z",
  version: 1,
  members: [
    {
      fingerprint: "signal-1",
      labels: {
        namespace: "payments",
        alertname: "KubePodCrashLooping",
        severity: "warning",
      },
      annotations: { summary: "Container repeatedly restarts" },
      status: { state: "active" },
    },
  ],
  timeline: [
    {
      at: "2026-01-01T00:00:00Z",
      actor: "system",
      kind: "opened",
      detail: "Observed firing signal",
    },
  ],
};
async function base(page: import("@playwright/test").Page, readOnly = false) {
  await page.route("**/api/context", (r) =>
    r.fulfill({
      json: {
        ...context,
        readOnly,
        tenantMode: readOnly,
        namespaces: readOnly ? ["payments"] : null,
      },
    }),
  );
  await page.route("**/api/alerts", (r) => r.fulfill({ json: [] }));
  await page.route("**/api/silences", (r) => r.fulfill({ json: [] }));
}
test("analytics distinguishes unknown coverage, supports namespace selection and bounded window filters", async ({
  page,
}, testInfo) => {
  await base(page);
  const requests: string[] = [];
  await page.route("**/api/analytics?*", (route) => {
    requests.push(route.request().url());
    return route.fulfill({
      json: {
        windowDays: 7,
        start: "2026-10-25T00:00:00Z",
        end: "2026-10-31T12:00:00Z",
        generatedAt: "2026-10-31T12:00:00Z",
        timeZone: "UTC",
        resolutionSeconds: 60,
        incomplete: true,
        warnings: ["One day has unknown coverage"],
        coverage: {
          knownSeconds: 86400,
          expectedSeconds: 172800,
          percent: 50,
          kind: "source",
        },
        namespaces: [
          {
            namespace: "payments",
            knownSeconds: 86400,
            firingSeconds: 3600,
            firingPercent: 4.17,
            episodes: 8,
            daysWithAlerts: 1,
            days: [
              {
                date: "2026-10-30",
                knownSeconds: 0,
                expectedSeconds: 86400,
                firingSeconds: 0,
                firingPercent: null,
                episodes: 0,
              },
              {
                date: "2026-10-31",
                knownSeconds: 86400,
                expectedSeconds: 86400,
                firingSeconds: 3600,
                firingPercent: 4.17,
                episodes: 8,
              },
            ],
          },
        ],
      },
    });
  });
  await page.goto("/?view=analytics");
  await expect(
    page.getByRole("heading", { name: "Where alerts keep coming back." }),
  ).toBeVisible();
  await expect(
    page.getByText("Incomplete history", { exact: true }),
  ).toBeVisible();
  await expect(page.locator("button.heat-cell.unknown")).toHaveCount(1);
  await page.getByText("Accessible daily data", { exact: true }).click();
  await expect(
    page.getByRole("cell", { name: "Unknown", exact: true }),
  ).toHaveCount(2);
  await page
    .getByRole("combobox", { name: "Window", exact: true })
    .selectOption("30");
  await expect.poll(() => requests.at(-1)).toContain("days=30");
  await page
    .getByRole("combobox", { name: "Severity", exact: true })
    .selectOption("critical");
  await expect.poll(() => requests.at(-1)).toContain("severity=critical");
  await page.screenshot({
    path: testInfo.outputPath("analytics-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: testInfo.outputPath("analytics-mobile.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});
test("incident follow-up preserves source evidence and submits versioned operator changes", async ({
  page,
}) => {
  await base(page);
  let current = { ...incident };
  let mutation: unknown;
  await page.route("**/api/incidents?*", (r) =>
    r.fulfill({
      json: {
        incidents: [current],
        source: { state: "fresh", lastSuccess: "2026-10-31T12:00:00Z" },
        readOnly: false,
      },
    }),
  );
  await page.route("**/api/incidents/case-1", (r) => {
    if (r.request().method() === "PATCH") {
      mutation = r.request().postDataJSON();
      current = {
        ...current,
        ...r.request().postDataJSON(),
        version: current.version + 1,
      };
    }
    return r.fulfill({ json: current });
  });
  await page.goto("/?view=incidents");
  await page
    .getByRole("combobox", { name: "Follow-up", exact: true })
    .selectOption("overdue");
  await expect(
    page.getByRole("heading", { name: incident.title }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Investigate →" }).click();
  await page.getByRole("button", { name: "Evidence", exact: true }).click();
  await page
    .locator("summary")
    .getByText("KubePodCrashLooping", { exact: true })
    .click();
  await expect(
    page.getByText("Container repeatedly restarts", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Response", exact: true }).click();
  await page.getByLabel("Owner", { exact: true }).fill("Platform");
  await page.getByRole("button", { name: "Save follow-up" }).click();
  await expect
    .poll(() => mutation)
    .toEqual({
      version: 1,
      owner: "Platform",
      reviewAt: "2026-01-02T00:00:00.000Z",
    });
  await expect(
    page.getByRole("button", { name: "Close cleared incident" }),
  ).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
});
test("tenant incident detail is read-only and upstream loss does not show recovery", async ({
  page,
}) => {
  await base(page, true);
  await page.route("**/api/incidents?*", (r) =>
    r.fulfill({
      json: {
        incidents: [incident],
        source: { state: "stale", lastSuccess: "2026-10-31T12:00:00Z" },
        readOnly: true,
      },
    }),
  );
  await page.route("**/api/incidents/case-1", (r) =>
    r.fulfill({ json: incident }),
  );
  await page.goto("/?view=incidents");
  await expect(page.getByText(/No recovery inferred/)).toBeVisible();
  await page.getByRole("button", { name: "Investigate →" }).click();
  await page.getByRole("button", { name: "Response", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Save follow-up" }),
  ).toHaveCount(0);
  await expect(page.getByText(/Read-only access/)).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});

test("webhook incident link opens the correct detail and can return to inbox", async ({
  page,
}) => {
  await base(page);
  await page.route("**/api/incidents?*", (r) =>
    r.fulfill({
      json: {
        incidents: [incident],
        source: { state: "connected" },
        readOnly: false,
      },
    }),
  );
  await page.route("**/api/incidents/case-1", (r) =>
    r.fulfill({ json: incident }),
  );
  await page.goto("/?incident=case-1");
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(
    page.getByRole("dialog").getByRole("heading", { name: incident.title }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Close incident", exact: true })
    .click();
  await expect(page).not.toHaveURL(/incident=/);
  await expect(
    page.getByRole("heading", { name: "What still needs attention." }),
  ).toBeVisible();
});

test("incident pagination labels loaded scope and can find work beyond the first page", async ({
  page,
}) => {
  await base(page);
  await page.route("**/api/incidents?*", (route) => {
    const offset = Number(
      new URL(route.request().url()).searchParams.get("offset"),
    );
    return route.fulfill({
      json: {
        incidents:
          offset === 0
            ? Array.from({ length: 200 }, (_, i) => ({
                ...incident,
                id: `case-${i}`,
                title: `Routine issue ${i}`,
              }))
            : [{ ...incident, id: "last", title: "Oldest import issue" }],
        source: { state: "connected" },
        readOnly: false,
        total: 201,
        limit: 200,
        offset,
      },
    });
  });
  await page.goto("/?view=incidents");
  await expect(page.getByText(/200 \/ 201 incidents loaded/)).toBeVisible();
  await page
    .getByRole("searchbox", { name: "Search incidents" })
    .fill("Oldest import");
  await expect(
    page.getByRole("heading", { name: "Oldest import issue" }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Load more incidents" }).click();
  await expect(
    page.getByRole("heading", { name: "Oldest import issue" }),
  ).toBeVisible();
  await expect(page.getByText(/201 \/ 201 incidents loaded/)).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Load more incidents" }),
  ).toHaveCount(0);
});

test("shadow evaluations are reviewable without implying applied ranking", async ({
  page,
}) => {
  await base(page);
  await page.route("**/api/context", (r) =>
    r.fulfill({ json: { ...context, triageEnabled: true } }),
  );
  await page.route("**/api/incidents?*", (r) =>
    r.fulfill({
      json: {
        incidents: [incident],
        source: { state: "connected" },
        readOnly: false,
        total: 1,
        limit: 200,
        offset: 0,
      },
    }),
  );
  await page.route("**/api/triage/status", (r) =>
    r.fulfill({
      json: {
        enabled: true,
        apply: false,
        state: "shadow",
        monthlyBudgetUSD: 10,
        usage: {
          month: "2026-10",
          reservedUSD: 0,
          spentUSD: 0.01,
          inputTokens: 2000,
          requests: 1,
        },
        decisions: [
          {
            incidentId: incident.id,
            inputHash: "hash",
            model: "typesafe/jev-1.13",
            policyVersion: "v1",
            createdAt: "2026-10-31T12:00:00Z",
            applied: false,
            decisions: {
              urgency: { choice: "immediate" },
              membership: { choice: "none" },
            },
          },
        ],
      },
    }),
  );
  await page.goto("/?view=incidents");
  await page.getByText("Optional Jev triage · shadow", { exact: true }).click();
  await expect(
    page.getByText(/Evaluation only. Model decisions do not change/),
  ).toBeVisible();
  await page.locator("summary").getByText(incident.id, { exact: true }).click();
  await expect(page.getByText("immediate", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Inspect current incident evidence →" }),
  ).toHaveAttribute("href", "?view=incidents&incident=case-1");
});

test("long unresolved queues collapse by namespace while urgent incidents stay visible", async ({
  page,
}) => {
  await base(page);
  await page.route("**/api/incidents?*", (route) =>
    route.fulfill({
      json: {
        incidents: [
          ...Array.from({ length: 8 }, (_, i) => ({
            ...incident,
            id: `grouped-${i}`,
            namespace: i < 4 ? "payments" : "imports",
            title: `Namespace issue ${i}`,
            sourceSeverity: "warning",
            urgency: "today",
          })),
          {
            ...incident,
            id: "critical-now",
            title: "Storage requires action",
            sourceSeverity: "critical",
            urgency: "immediate",
          },
        ],
        source: { state: "connected" },
        readOnly: false,
        total: 9,
        limit: 200,
        offset: 0,
      },
    }),
  );
  await page.goto("/?view=incidents");
  await expect(
    page.getByRole("heading", { name: "Storage requires action", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".incident-namespace")).toHaveCount(2);
  await expect(
    page.getByRole("heading", { name: "Namespace issue 0", exact: true }),
  ).not.toBeVisible();
  await page
    .locator(".incident-namespace summary")
    .filter({ hasText: "payments" })
    .click();
  await expect(
    page.getByRole("heading", { name: "Namespace issue 0", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Namespace issue 4", exact: true }),
  ).not.toBeVisible();
  await page
    .getByRole("searchbox", { name: "Search incidents" })
    .fill("Namespace issue 0");
  await expect(page.locator(".incident-namespace")).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "Namespace issue 0", exact: true }),
  ).toBeVisible();
});

test("the most recently selected incident wins when detail responses arrive out of order", async ({
  page,
}) => {
  await base(page);
  const older = {
    ...incident,
    id: "slow-incident",
    title: "Earlier selection",
  };
  const newer = {
    ...incident,
    id: "latest-incident",
    title: "Latest selection",
  };
  await page.route("**/api/incidents?*", (r) =>
    r.fulfill({
      json: {
        incidents: [older, newer],
        source: { state: "connected" },
        readOnly: false,
        total: 2,
        limit: 200,
        offset: 0,
      },
    }),
  );
  let release!: () => void;
  const delayed = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/api/incidents/slow-incident", async (r) => {
    await delayed;
    await r.fulfill({ json: older });
  });
  await page.route("**/api/incidents/latest-incident", (r) =>
    r.fulfill({ json: newer }),
  );
  await page.goto("/?view=incidents");
  const slowRequest = page.waitForRequest((r) =>
    r.url().endsWith("/api/incidents/slow-incident"),
  );
  await page
    .locator("article")
    .filter({ hasText: "Earlier selection" })
    .getByRole("button", { name: "Investigate →" })
    .click();
  await slowRequest;
  await page
    .locator("article")
    .filter({ hasText: "Latest selection" })
    .getByRole("button", { name: "Investigate →" })
    .click();
  await expect(
    page
      .getByRole("dialog")
      .getByRole("heading", { name: "Latest selection", exact: true }),
  ).toBeVisible();
  const slowResponse = page.waitForResponse((r) =>
    r.url().endsWith("/api/incidents/slow-incident"),
  );
  release();
  await slowResponse;
  // Flush queued response handling and React rendering, without a timing sleep.
  await page.evaluate(
    () =>
      new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      ),
  );
  await expect(
    page
      .getByRole("dialog")
      .getByRole("heading", { name: "Latest selection", exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("dialog")
      .getByRole("heading", { name: "Earlier selection", exact: true }),
  ).toHaveCount(0);
});

test("saving an incident does not reopen its closed dialog", async ({
  page,
}) => {
  await base(page);
  await page.route("**/api/incidents?*", (r) =>
    r.fulfill({
      json: {
        incidents: [incident],
        source: { state: "connected" },
        readOnly: false,
        total: 1,
      },
    }),
  );
  let release!: () => void;
  const delayed = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/incidents/case-1", async (r) => {
    if (r.request().method() === "PATCH") {
      await delayed;
      await r.fulfill({ json: { ...incident, owner: "Platform", version: 2 } });
    } else await r.fulfill({ json: incident });
  });
  await page.goto("/?view=incidents");
  await page.getByRole("button", { name: "Investigate →" }).click();
  await page.getByRole("button", { name: "Response", exact: true }).click();
  await page.getByLabel("Owner", { exact: true }).fill("Platform");
  const request = page.waitForRequest((r) => r.method() === "PATCH");
  await page.getByRole("button", { name: "Save follow-up" }).click();
  await request;
  await page
    .getByRole("button", { name: "Close incident", exact: true })
    .click();
  release();
  await expect(
    page.getByText(
      "Incident updated. Existing Alertmanager notifications are unchanged.",
    ),
  ).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("incident detail failure preserves the list and retry opens the incident", async ({
  page,
}) => {
  await base(page);
  await page.route("**/api/incidents?*", (r) =>
    r.fulfill({
      json: {
        incidents: [incident],
        source: { state: "connected" },
        readOnly: false,
        total: 1,
      },
    }),
  );
  let failDetail = true;
  await page.route("**/api/incidents/case-1", (r) =>
    failDetail
      ? r.fulfill({ status: 502, json: { error: "Detail source unavailable" } })
      : r.fulfill({ json: incident }),
  );
  await page.goto("/?view=incidents");
  await page.getByRole("button", { name: "Investigate →" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Cannot load incident details",
  );
  await expect(page.getByRole("alert")).toContainText(
    "Detail source unavailable",
  );
  await expect(
    page.getByText("Incident data is stale", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: incident.title, exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  failDetail = false;
  await page.getByRole("button", { name: "Investigate →" }).click();
  await expect(
    page
      .getByRole("dialog")
      .getByRole("heading", { name: incident.title, exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
});
