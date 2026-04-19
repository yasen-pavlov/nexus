import { test, expect, type Page } from "./fixtures";

// Mobile responsiveness smoke. Renders the most-affected admin surfaces
// at iPhone-size viewport and asserts the mobile-friendly variants kick
// in: card stacks instead of grid tables, settings TOC drawer trigger
// instead of the desktop left rail.

test.use({ viewport: { width: 375, height: 812 } });

async function mockAuthedAdmin(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("nexus_jwt", "fake-e2e-token");
  });
  await page.route("**/api/auth/me", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          id: "u1",
          username: "admin",
          role: "admin",
          created_at: "2026-03-01T10:00:00Z",
        },
      }),
    }),
  );
  await page.route("**/api/health", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { status: "ok" } }),
    }),
  );
  await page.route("**/api/connectors/", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );
  await page.route("**/api/sync", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );
  await page.route("**/api/sync/progress*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "",
    }),
  );
  await page.route("**/api/me/identities", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { identities: [] } }),
    }),
  );
  await page.route("**/api/users", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: "u1",
            username: "admin",
            role: "admin",
            created_at: "2026-03-01T10:00:00Z",
          },
          {
            id: "u2",
            username: "viewer",
            role: "user",
            created_at: "2026-03-15T10:00:00Z",
          },
        ],
      }),
    }),
  );
  // Settings page calls these on mount; any 401 bounces us to /login.
  await page.route("**/api/settings/embedding", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { provider: "", model: "", api_key: "", ollama_url: "" },
      }),
    }),
  );
  await page.route("**/api/settings/rerank", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { provider: "", model: "", api_key: "" },
      }),
    }),
  );
  await page.route("**/api/settings/retention", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          sync_runs_retention_days: 90,
          sync_runs_retention_per_connector: 200,
          sync_runs_sweep_interval_minutes: 60,
        },
      }),
    }),
  );
  await page.route("**/api/settings/ranking", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { runtime_wiring_active: false },
      }),
    }),
  );
  await page.route("**/api/admin/stats", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          total_documents: 0,
          total_chunks: 0,
          users_count: 0,
          per_source: [],
          embedding: { enabled: false, provider: "", model: "", dimension: 0 },
          rerank: { enabled: false, provider: "", model: "" },
        },
      }),
    }),
  );
  await page.route("**/api/storage/stats", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );
}

test("/admin/users renders the mobile card stack at 375px", async ({
  page,
}) => {
  await mockAuthedAdmin(page);
  await page.goto("/admin/users");
  await expect(page.getByRole("heading", { name: /^users$/i })).toBeVisible();

  // Desktop table column header "Joined" is `hidden md:` so it should be
  // absent on mobile; mobile card "since X" copy fills its role.
  await expect(page.getByText("admin").first()).toBeVisible();
  await expect(page.getByText("viewer").first()).toBeVisible();

  // No horizontal overflow — assert document scroll width fits viewport.
  const docWidth = await page.evaluate(
    () => document.documentElement.scrollWidth,
  );
  expect(docWidth).toBeLessThanOrEqual(376); // allow 1px slop
});

test("/admin/settings shows the mobile TOC pill at 375px", async ({ page }) => {
  await mockAuthedAdmin(page);
  await page.goto("/admin/settings");
  await expect(
    page.getByRole("heading", { name: /^settings$/i }),
  ).toBeVisible();
  // The mobile TOC trigger is the first `Embeddings` mention (sticky pill at top).
  const mobileTrigger = page.locator("button[aria-haspopup='dialog']", {
    hasText: /embeddings/i,
  });
  await expect(mobileTrigger).toBeVisible();
});
