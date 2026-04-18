import { test, expect, type Page } from "@playwright/test";

// High-value E2E: the Schedule tab must round-trip — set Weekly on a
// specific day, save, reload, and see the preset re-detected as
// Weekly with the same day selected. This tests both the detectPreset
// regex and the PUT /connectors/:id → GET /connectors/:id path.

async function mockAuthedBase(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("nexus_jwt", "fake-e2e-token");
  });
  await page.route("**/api/auth/me", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { id: "u1", username: "e2e", role: "admin" } }),
    }),
  );
  await page.route("**/api/health", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { status: "ok" } }),
    }),
  );
  await page.route("**/api/me/identities", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { identities: [] } }),
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
    route.fulfill({ status: 200, contentType: "text/event-stream", body: "" }),
  );
}

test("schedule tab round-trips a Weekly preset through PUT → GET", async ({ page }) => {
  await mockAuthedBase(page);

  const mutable = {
    id: "c-1",
    type: "filesystem",
    name: "notes",
    config: { root_path: "/tmp" },
    enabled: true,
    schedule: "",
    shared: false,
    status: "active",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    last_run: null,
    user_id: "u1",
    external_id: "",
    external_name: "",
  };

  await page.route("**/api/connectors/", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [mutable] }),
    }),
  );
  await page.route("**/api/connectors/c-1", (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON() as { schedule?: string };
      if (typeof body.schedule === "string") {
        mutable.schedule = body.schedule;
      }
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ data: mutable }),
      });
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: mutable }),
    });
  });
  await page.route("**/api/connectors/c-1/runs*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );

  await page.goto("/connectors/c-1");

  // Flip to Schedule tab.
  await page.getByRole("tab", { name: "Schedule" }).click();

  // Initial preset is Off because schedule is "".
  const offTab = page.getByRole("tab", { name: "Off" });
  await expect(offTab).toHaveAttribute("aria-selected", "true");

  // Pick Weekly. The preset seeds Monday at 09:00 → cron "0 9 * * 1".
  await page.getByRole("tab", { name: "Weekly" }).click();
  const weekly = page.getByRole("tab", { name: "Weekly" });
  await expect(weekly).toHaveAttribute("aria-selected", "true");

  // Persist. ScheduleField fires onChange → the detail page's inline
  // onChange calls updateConnector. Give the PUT a moment to land.
  await expect(async () => {
    expect(mutable.schedule).toMatch(/^\d+ \d+ \* \* \d+(,\d+)*$/);
  }).toPass({ timeout: 3000 });

  // Reload. The GET returns the stored schedule; detectPreset should
  // classify it as Weekly again, and Monday should stay selected.
  await page.reload();
  await page.getByRole("tab", { name: "Schedule" }).click();
  await expect(page.getByRole("tab", { name: "Weekly" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByRole("button", { name: "Mon" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
});
