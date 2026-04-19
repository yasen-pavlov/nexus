import { test, expect, type Page } from "@playwright/test";

async function mockAuthed(page: Page) {
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
}

test("404 page renders for an unknown route, Go home link works", async ({
  page,
}) => {
  await mockAuthed(page);
  await page.goto("/totally-bogus-path");
  await expect(page.getByText(/we couldn't find that page/i)).toBeVisible();
  await expect(page.getByText(/404 · off the map/i)).toBeVisible();
  await page.getByRole("link", { name: /go home/i }).click();
  await expect(page).toHaveURL(/\/$|\/\?/);
});
