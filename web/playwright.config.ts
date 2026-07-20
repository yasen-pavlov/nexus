import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  // Route modules compile on-demand in the vite dev server. Under the coverage
  // run (8 parallel workers + per-test page.coverage overhead on a loaded
  // machine) a cold first-navigation can blow the 30s default timeout even
  // though the test is logically fine — it passes in isolation and in unloaded
  // full runs. Give tests real headroom (the vite `server.warmup` config
  // pre-compiles routes to cut the spikes at the source).
  timeout: 60_000,
  // E2E is now gating (coverage:all fails the job on any e2e failure), so give
  // genuinely flaky runs retries before failing. One locally (where developers
  // run coverage:all), two in CI.
  retries: process.env.CI ? 2 : 1,
  use: {
    baseURL: "http://localhost:5174",
  },
  webServer: {
    command: "npm run dev",
    port: 5174,
    reuseExistingServer: !process.env.CI,
  },
});
