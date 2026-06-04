import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  // E2E is now gating (coverage:all fails the job on any e2e failure), so give
  // genuinely flaky runs a couple of retries in CI before failing the build.
  retries: process.env.CI ? 2 : 0,
  use: {
    baseURL: "http://localhost:5174",
  },
  webServer: {
    command: "npm run dev",
    port: 5174,
    reuseExistingServer: !process.env.CI,
  },
});
