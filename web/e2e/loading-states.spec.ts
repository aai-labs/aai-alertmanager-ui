import { test, expect, type Page } from "@playwright/test";
const context = {
  readOnly: true,
  tenantMode: false,
  identity: "operator",
  namespaces: [],
  prometheusEnabled: true,
  demo: true,
  priorities: [],
  analyticsEnabled: true,
  incidentsEnabled: true,
  resourcesEnabled: true,
  workloadsEnabled: true,
};
async function setup(page: Page) {
  await page.route("**/api/context", (r) => r.fulfill({ json: context }));
  await page.route("**/api/alerts", (r) => r.fulfill({ json: [] }));
  await page.route("**/api/prometheus", (r) =>
    r.fulfill({ json: { data: { alerts: [] } } }),
  );
  await page.route("**/api/silences", (r) => r.fulfill({ json: [] }));
}
test("configuration discovery does not flash disabled feature messages", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route("**/api/context", async (r) => {
    await gate;
    await r.fulfill({ status: 503, json: { error: "unavailable" } });
  });
  await page.goto("/?view=resources");
  await expect(
    page.getByText("Loading application…", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/requires a configured Prometheus/)).toHaveCount(
    0,
  );
  release();
  await expect(
    page.getByText("Application unavailable", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Loading application…", { exact: true }),
  ).toHaveCount(0);
});
test("resources distinguish pending queries from empty and missing history", async ({
  page,
}) => {
  await setup(page);
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route("**/api/resources?*", async (r) => {
    await gate;
    await r.fulfill({
      json: {
        generatedAt: "2026-11-01T12:00:00Z",
        hours: 24,
        resolutionSeconds: 300,
        incomplete: false,
        warnings: [],
        notes: [],
        cluster: null,
        nodes: [],
        namespaces: [],
      },
    });
  });
  await page.goto("/?view=resources");
  await expect(
    page.getByText("Loading resource history…", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("No historical resource samples available.", {
      exact: true,
    }),
  ).toHaveCount(0);
  release();
  await expect(
    page.getByText("No resource observations", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("No historical resource samples available.", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("No volume metrics observed", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Loading resource history…", { exact: true }),
  ).toHaveCount(0);
});
test("workload loading and failures never become an empty inventory", async ({
  page,
}) => {
  await setup(page);
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route("**/api/workloads?*", async (r) => {
    await gate;
    await r.fulfill({ status: 502, json: { error: "upstream unavailable" } });
  });
  await page.goto("/?view=workloads");
  await expect(
    page.getByText("Loading workload inventory…", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/No workload inventory observed/)).toHaveCount(0);
  release();
  await expect(
    page.getByText("Workload inventory unavailable: upstream unavailable", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByText(/No workload inventory observed/)).toHaveCount(0);
  await expect(
    page.getByText("Loading workload inventory…", { exact: true }),
  ).toHaveCount(0);
});
test("silences stay loading until the initial response completes", async ({
  page,
}) => {
  await setup(page);
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route("**/api/silences", async (r) => {
    await gate;
    await r.fulfill({ json: [] });
  });
  await page.goto("/?view=silences");
  await expect(
    page.getByText("Loading silences…", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("No silences", { exact: true })).toHaveCount(0);
  release();
  await expect(page.getByText("No silences", { exact: true })).toBeVisible();
});
