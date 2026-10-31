import { test, expect } from "@playwright/test";
test.afterEach(async ({ page }) => {
  await page.unrouteAll({ behavior: "wait" });
});
const screenshots = process.env.UPDATE_SCREENSHOTS
  ? "../docs/images"
  : "test-results/screenshots";

test("focus keeps important and unfamiliar alerts visible; routine signals are collapsed", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "storage", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "payments", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".bucket").filter({ hasText: "Job failures" }),
  ).not.toHaveAttribute("open", "");
  await expect(page.getByRole("button", { name: "New silence" })).toHaveCount(
    0,
  );
  await page
    .locator(".alert-card")
    .filter({ hasText: "storage" })
    .locator("summary")
    .first()
    .click();
  await expect(
    page.getByText("Preset: CephHealthError", { exact: true }),
  ).toBeVisible();
  await page.getByRole("searchbox").fill("namespace=payments");
  await expect(page.locator(".alert-card")).toHaveCount(1);
  await expect(page).toHaveURL(/q=namespace%3Dpayments/);
  await page.reload();
  await expect(page.locator(".alert-card")).toHaveCount(1);
  await expect(
    page.getByRole("heading", { name: "payments", exact: true }),
  ).toBeVisible();
  expect(errors).toEqual([]);
});

test("all-alerts view and namespaces preserve access to suppressed data", async ({
  page,
}) => {
  await page.goto("/?view=all&namespace=staging");
  await expect(page.locator(".alert-card")).toHaveCount(2);
  await page
    .locator(".alert-card")
    .filter({ hasText: "Suppressed" })
    .locator("summary")
    .first()
    .click();
  await expect(
    page.getByText("DatabaseReplicationLag", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("critical · Silenced")).toBeVisible();
});

test("operator can preview, create, edit and expire silences", async ({
  page,
}) => {
  let saved: any = null;
  let deleted = false;
  await page.route("**/api/context", async (route) => {
    const response = await route.fetch();
    await route.fulfill({
      json: { ...(await response.json()), demo: false, readOnly: false },
    });
  });
  await page.route("**/api/silences", async (route) => {
    if (route.request().method() === "POST") {
      saved = route.request().postDataJSON();
      await route.fulfill({ json: { silenceID: "new-id" } });
    } else await route.continue();
  });
  await page.route("**/api/silences/*", async (route) => {
    if (route.request().method() === "DELETE") {
      deleted = true;
      await route.fulfill({ status: 200, json: {} });
    } else await route.continue();
  });
  await page.goto("/?view=silences");
  await page.getByRole("button", { name: "New silence" }).click();
  await page
    .getByRole("textbox", { name: "Matcher 1 label" })
    .fill("alertname");
  await page
    .getByRole("textbox", { name: "Matcher 1 value" })
    .fill("CephHealthError");
  await page.getByLabel("Created by").fill("test-operator");
  await page.getByLabel("Reason").fill("Planned storage maintenance");
  await expect(
    page.getByText("1 matching alerts", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Save silence" }),
  ).toBeDisabled();
  await page.screenshot({
    path: `${screenshots}/silence-editor.png`,
    fullPage: true,
  });
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Save silence" }).click();
  await expect(page.getByText("Silence saved.", { exact: true })).toBeVisible();
  expect(saved.matchers[0].value).toBe("CephHealthError");
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await page.getByLabel("Reason").fill("Updated maintenance");
  await expect(
    page.getByText("4 matching alerts", { exact: true }),
  ).toBeVisible();
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Save silence" }).click();
  await expect(page.locator("dialog")).toHaveCount(0);
  expect(saved.id).toBeTruthy();
  await page.getByRole("button", { name: "Expire", exact: true }).click();
  await page
    .getByRole("button", { name: "Expire silence", exact: true })
    .click();
  await expect(
    page.getByText("Silence expired.", { exact: true }),
  ).toBeVisible();
  expect(deleted).toBe(true);
});

test("connection failure retains a clearly stale snapshot", async ({
  page,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "storage", exact: true }),
  ).toBeVisible();
  await page.route("**/api/alerts", (route) =>
    route.fulfill({ status: 502, json: { error: "Alertmanager unavailable" } }),
  );
  await page.getByRole("button", { name: "Refresh alerts" }).click();
  await expect(page.getByText("Alert data is stale")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "storage", exact: true }),
  ).toBeVisible();
});

test("tenant view has no operator controls or cluster-wide filter", async ({
  page,
}) => {
  await page.route("**/api/context", (route) =>
    route.fulfill({
      json: {
        tenantMode: true,
        readOnly: true,
        identity: "alice",
        namespaces: ["storefront"],
        priorities: [],
        prometheusEnabled: false,
        demo: false,
      },
    }),
  );
  await page.route("**/api/alerts", async (route) => {
    const response = await route.fetch();
    await route.fulfill({
      json: (await response.json()).filter(
        (a: any) => a.labels.namespace === "storefront",
      ),
    });
  });
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "storefront", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /Silences/ })).toHaveCount(0);
  await expect(page.getByRole("option", { name: "Cluster-wide" })).toHaveCount(
    0,
  );
});

test("documentation screenshots and mobile layout", async ({ page }) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "storage", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({
    path: `${screenshots}/dashboard.png`,
    fullPage: true,
  });
  await page
    .locator(".alert-card")
    .filter({ hasText: "storage" })
    .locator("summary")
    .first()
    .click();
  await page.screenshot({
    path: `${screenshots}/alert-details.png`,
    fullPage: true,
  });
  await page.getByRole("button", { name: /Silences/ }).click();
  await page.screenshot({
    path: `${screenshots}/silences.png`,
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "storage", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({ path: `${screenshots}/mobile.png`, fullPage: true });
});
