import { test, expect } from "@playwright/test";
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

test("browser manages a silence through real Alertmanager", async ({
  page,
  request,
}) => {
  test.setTimeout(60000);
  const folder = mkdtempSync(join(tmpdir(), "alerts-ui-browser-"));
  const name = `alerts-ui-browser-${process.pid}`;
  let app: ChildProcess | undefined;
  try {
    writeFileSync(
      join(folder, "alertmanager.yml"),
      "route:\n  receiver: noop\nreceivers:\n  - name: noop\n",
    );
    execFileSync(
      "docker",
      [
        "create",
        "--name",
        name,
        "-p",
        "127.0.0.1::9093",
        "quay.io/prometheus/alertmanager:v0.34.1",
        "--config.file=/etc/alertmanager/test.yml",
        "--storage.path=/alertmanager",
        "--cluster.listen-address=",
      ],
      { stdio: "pipe" },
    );
    execFileSync("docker", [
      "cp",
      join(folder, "alertmanager.yml"),
      `${name}:/etc/alertmanager/test.yml`,
    ]);
    execFileSync("docker", ["start", name], { stdio: "pipe" });
    const upstream =
      "http://" +
      execFileSync("docker", ["port", name, "9093/tcp"], {
        encoding: "utf8",
      }).trim();
    await expect
      .poll(async () => {
        try {
          return (await request.get(upstream + "/-/ready")).status();
        } catch {
          return 0;
        }
      })
      .toBe(200);
    await request.post(upstream + "/api/v2/alerts", {
      data: [
        {
          labels: {
            alertname: "BrowserTestAlert",
            namespace: "browser-test",
            severity: "warning",
          },
          annotations: { summary: "Fictional browser integration alert" },
          startsAt: new Date().toISOString(),
          endsAt: new Date(Date.now() + 3600000).toISOString(),
        },
      ],
    });
    app = spawn(resolve("../.build/alertmanager-ui"), [], {
      env: {
        ...process.env,
        CONFIG_FILE: "",
        DEMO: "false",
        READ_ONLY: "false",
        BASE_PATH: "",
        PROMETHEUS_URL: "",
        ALERTMANAGER_URL: upstream,
        LISTEN_ADDR: "127.0.0.1:18083",
      },
      stdio: "ignore",
    });
    const base = "http://127.0.0.1:18083";
    await expect
      .poll(async () => {
        try {
          return (await request.get(base + "/healthz")).status();
        } catch {
          return 0;
        }
      })
      .toBe(200);
    await page.goto(base + "/?view=silences");
    await page.getByRole("button", { name: "New silence" }).click();
    await page
      .getByRole("textbox", { name: "Matcher 1 label" })
      .fill("alertname");
    await page
      .getByRole("textbox", { name: "Matcher 1 value" })
      .fill("BrowserTestAlert");
    await page.getByLabel("Reason").fill("Browser integration maintenance");
    await expect(
      page.getByText("1 matching alerts", { exact: true }),
    ).toBeVisible();
    await page.getByRole("checkbox").check();
    await page.getByRole("button", { name: "Save silence" }).click();
    await expect(
      page.getByRole("heading", {
        name: "Browser integration maintenance",
        exact: true,
      }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    await page.getByLabel("Reason").fill("Updated browser maintenance");
    await expect(
      page.getByText("1 matching alerts", { exact: true }),
    ).toBeVisible();
    await page.getByRole("checkbox").check();
    await page.getByRole("button", { name: "Save silence" }).click();
    await expect(
      page.getByRole("heading", {
        name: "Updated browser maintenance",
        exact: true,
      }),
    ).toBeVisible();
    const card = page.locator(".silence-card").filter({
      has: page.getByRole("heading", {
        name: "Updated browser maintenance",
        exact: true,
      }),
    });
    await card.getByRole("button", { name: "Expire", exact: true }).click();
    await page
      .getByRole("button", { name: "Expire silence", exact: true })
      .click();
    await expect(card.locator(".badge")).toHaveText("expired");
  } finally {
    if (app && app.exitCode === null) {
      const exited = new Promise<void>((resolve) =>
        app!.once("exit", () => resolve()),
      );
      app.kill("SIGTERM");
      await exited;
    }
    try {
      execFileSync("docker", ["rm", "--force", name], { stdio: "ignore" });
    } catch {
      /* Already removed on exit. */
    }
    rmSync(folder, { recursive: true, force: true });
  }
});
