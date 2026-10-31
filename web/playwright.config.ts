import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  use: {
    baseURL: "http://127.0.0.1:18080",
    browserName: "chromium",
    viewport: { width: 1440, height: 1100 },
  },
  webServer: {
    command: "DEMO=true LISTEN_ADDR=127.0.0.1:18080 ../.build/alertmanager-ui",
    url: "http://127.0.0.1:18080/healthz",
    reuseExistingServer: false,
  },
  reporter: "list",
});
