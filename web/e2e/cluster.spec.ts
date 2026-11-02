import { test, expect, type Page } from "@playwright/test";
const context = {
  readOnly: true,
  tenantMode: false,
  identity: "operator",
  namespaces: null,
  prometheusEnabled: true,
  demo: true,
  priorities: [],
  analyticsEnabled: true,
  incidentsEnabled: true,
  resourcesEnabled: true,
  workloadsEnabled: true,
};
const trend = [
  {
    at: "2026-11-01T10:00:00Z",
    cpuUsageCores: 1,
    memoryUsageBytes: 1073741824,
    cpuRequestsCores: 2,
    memoryRequestsBytes: 2147483648,
  },
  {
    at: "2026-11-01T11:00:00Z",
    cpuUsageCores: null,
    memoryUsageBytes: null,
    cpuRequestsCores: 2,
    memoryRequestsBytes: 2147483648,
  },
  {
    at: "2026-11-01T12:00:00Z",
    cpuUsageCores: 1.5,
    memoryUsageBytes: 1073741824,
    cpuRequestsCores: 2,
    memoryRequestsBytes: 2147483648,
  },
];
const resources = {
  generatedAt: "2026-11-01T12:00:00Z",
  hours: 24,
  incomplete: true,
  warnings: ["Some containers have no configured limits"],
  resolutionSeconds: 300,
  cluster: {
    cpuAllocatableCores: 16,
    memoryAllocatableBytes: 34359738368,
    cpuUsageCores: 3,
    memoryUsageBytes: 4294967296,
  },
  nodes: [
    {
      node: "worker-1",
      cpuAllocatableCores: 16,
      memoryAllocatableBytes: 34359738368,
      cpuUsageCores: 3,
      memoryUsageBytes: 4294967296,
    },
  ],
  namespaces: [
    {
      namespace: "payments",
      cpuUsageCores: 1.5,
      memoryUsageBytes: 1073741824,
      cpuRequestsCores: 2,
      memoryRequestsBytes: 2147483648,
      cpuLimitsCores: null,
      memoryLimitsBytes: null,
      pvcRequestedBytes: 10737418240,
      pvcCapacityBytes: 10737418240,
      pvcUsedBytes: null,
      unlimitedCPUContainers: 1,
      unlimitedMemoryContainers: 1,
      trend,
      quotas: [
        { name: "team-quota", resource: "requests.cpu", hard: 10, used: 2 },
        { name: "second-quota", resource: "requests.cpu", hard: 5, used: 2 },
      ],
    },
  ],
};
const pod = (name: string, state: string) => ({
  name,
  phase: state === "completed" ? "Succeeded" : "Running",
  state,
  waitingReasons: state === "notReady" ? ["CrashLoopBackOff"] : [],
  node: "worker-1",
  ownerKind: "ReplicaSet",
  ownerName: "checkout-65d",
  cpuCores: state === "unknown" ? null : 0.2,
  memoryBytes: state === "unknown" ? null : 104857600,
  observedAt: "2026-11-01T12:00:00Z",
});
const workloads = {
  generatedAt: "2026-11-01T12:00:00Z",
  incomplete: false,
  warnings: [],
  source: { state: "current", maxAgeSeconds: 180 },
  namespaces: [
    {
      namespace: "payments",
      pods: [
        pod("checkout-ready", "ready"),
        pod("checkout-restarting", "notReady"),
        pod("checkout-unscheduled", "unscheduled"),
        pod("cleanup-complete", "completed"),
        pod("import-failed", "failed"),
        pod("unknown-readiness", "unknown"),
      ],
      controllers: [
        {
          kind: "Deployment",
          name: "checkout",
          ready: 1,
          desired: 2,
          available: 1,
        },
        {
          kind: "StatefulSet",
          name: "idle-db",
          ready: 0,
          desired: 0,
          available: 0,
        },
      ],
      services: [
        {
          name: "checkout-service",
          type: "ClusterIP",
          endpointsReady: null,
          reachability: "unknown",
          probeTargets: 0,
          failedProbeTargets: 0,
        },
      ],
      counts: {
        ready: 1,
        notReady: 1,
        unscheduled: 1,
        completed: 1,
        failed: 1,
        unknown: 1,
      },
    },
  ],
};
async function fixture(page: Page, tenant = false) {
  await page.route("**/api/context", (r) =>
    r.fulfill({
      json: {
        ...context,
        tenantMode: tenant,
        namespaces: tenant ? ["payments"] : null,
      },
    }),
  );
  await page.route("**/api/alerts", (r) => r.fulfill({ json: [] }));
  await page.route("**/api/prometheus", (r) =>
    r.fulfill({ json: { data: { alerts: [] } } }),
  );
  await page.route("**/api/silences", (r) => r.fulfill({ json: [] }));
  await page.route("**/api/resources?*", (r) => r.fulfill({ json: resources }));
  await page.route("**/api/workloads?*", (r) => r.fulfill({ json: workloads }));
  await page.route("**/api/analytics?*", (r) =>
    r.fulfill({
      json: {
        windowDays: 7,
        start: "2026-10-26",
        end: "2026-11-01",
        generatedAt: "2026-11-01",
        timeZone: "UTC",
        resolutionSeconds: 60,
        incomplete: true,
        warnings: ["No coverage"],
        coverage: {
          knownSeconds: 0,
          expectedSeconds: 86400,
          percent: null,
          kind: "source",
        },
        namespaces: [],
      },
    }),
  );
}
test("overview keeps existing alert views and exposes actionable pod states with contextual links", async ({
  page,
}, testInfo) => {
  await fixture(page);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/?view=overview");
  await expect(
    page.getByRole("button", { name: "Incident inbox", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Analytics", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("4 observed pods need attention", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".pod-grid .pod-tile")).toHaveCount(6);
  await page
    .getByRole("button", { name: "payments, checkout-restarting, Not ready" })
    .click();
  await expect(
    page.getByRole("dialog").getByText("Running", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("dialog").getByText("Not ready", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Namespace alerts →", exact: true }),
  ).toHaveAttribute("href", "?view=all&namespace=payments");
  await page.keyboard.press("Escape");
  await page.screenshot({
    path: testInfo.outputPath("overview-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: testInfo.outputPath("overview-mobile.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  expect(errors).toEqual([]);
});
test("resource limits and quotas remain distinct from usage and tenant node inventory is hidden", async ({
  page,
}) => {
  await fixture(page, true);
  const requests: string[] = [];
  await page.route("**/api/resources?*", (r) => {
    requests.push(r.request().url());
    return r.fulfill({ json: resources });
  });
  await page.goto("/?view=resources&namespace=payments");
  await expect(
    page.getByRole("heading", { name: "Resource demand, in context." }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Node utilization" }),
  ).toHaveCount(0);
  await expect(
    page.locator(".stat").filter({ hasText: "CPU limits" }).locator("strong"),
  ).toContainText("Unknown");
  await expect(page.getByText("team-quota", { exact: true })).toBeVisible();
  await expect(page.getByText("second-quota", { exact: true })).toBeVisible();
  await page
    .getByRole("combobox", { name: "Resource history" })
    .selectOption("168");
  await expect.poll(() => requests.at(-1)).toContain("hours=168");
  await expect.poll(() => requests.at(-1)).toContain("namespace=payments");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});
test("workloads distinguish completed, failed, not ready and unmeasured services", async ({
  page,
}) => {
  await fixture(page);
  await page.goto("/?view=workloads");
  await page
    .getByRole("combobox", { name: "Workload state" })
    .selectOption("attention");
  await expect(
    page.getByRole("button", { name: "cleanup-complete", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "import-failed", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "unknown-readiness", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Controllers", exact: true }).click();
  await expect(
    page.getByRole("cell", { name: "0 / 0", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Services", exact: true }).click();
  await expect(page.getByText("Not measured", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "Unknown", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(
      /A Service object and ready endpoints do not prove reachability/,
    ),
  ).toBeVisible();
});
test("source failure shows unavailable metrics instead of healthy zeros", async ({
  page,
}) => {
  await fixture(page);
  await page.route("**/api/resources?*", (r) =>
    r.fulfill({ status: 502, json: { error: "source unavailable" } }),
  );
  await page.route("**/api/workloads?*", (r) =>
    r.fulfill({ status: 502, json: { error: "source unavailable" } }),
  );
  await page.goto("/?view=overview");
  await expect(
    page.getByText("Resource metrics unavailable: source unavailable", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("Workload inventory unavailable: source unavailable", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("0 observed pods need attention", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page
      .locator(".stat")
      .filter({ hasText: "Workload CPU usage" })
      .locator("strong"),
  ).toContainText("Unknown");
});

test("partial namespace history keeps observed subtotals with reporting counts", async ({
  page,
}) => {
  await fixture(page);
  await page.route("**/api/resources?*", (r) =>
    r.fulfill({
      json: {
        ...resources,
        namespaces: [
          ...resources.namespaces,
          {
            ...resources.namespaces[0],
            namespace: "retired",
            cpuUsageCores: null,
            memoryUsageBytes: null,
            cpuRequestsCores: null,
            memoryRequestsBytes: null,
            trend: [],
          },
        ],
      },
    }),
  );
  await page.goto("/?view=overview");
  const cpu = page.locator(".stat").filter({ hasText: "Workload CPU usage" });
  await expect(cpu.locator("strong")).toContainText("1.5");
  await expect(
    cpu.getByText("Observed usage · 1/2 namespaces reporting", { exact: true }),
  ).toBeVisible();
  await page.getByText("Resource trend data", { exact: true }).click();
  await expect(
    page.getByRole("cell", { name: "1 / 2 namespaces", exact: true }).first(),
  ).toBeVisible();
  await page.getByRole("link", { name: "Find them →", exact: true }).click();
  await expect(
    page.getByRole("combobox", { name: "Workload state" }),
  ).toHaveValue("attention");
  await expect(
    page.getByRole("button", { name: "cleanup-complete", exact: true }),
  ).toHaveCount(0);
});
