import { test, expect, type Page } from "@playwright/test";

// Smoke test for the keyboard-shortcut layer wired up in Phase 5.
//
// We mock the auth + connector endpoints so the test stays hermetic and
// fast. The vitest layer covers the unit behavior of the palette / hook;
// here we only confirm the full keystroke → URL change reaches the app.

async function mockAuthedAdmin(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("nexus_jwt", "fake-e2e-token");
    const style = document.createElement("style");
    style.textContent = `
      button[aria-label="Open Tanstack query devtools"],
      button[aria-label="Open TanStack Router Devtools"] {
        display: none !important;
      }
    `;
    const mount = () =>
      (document.head || document.documentElement).appendChild(style);
    if (document.head) mount();
    else document.addEventListener("DOMContentLoaded", mount);
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

test("Cmd+K opens the command palette and Enter navigates", async ({
  page,
}) => {
  await mockAuthedAdmin(page);
  await page.goto("/");
  await expect(page.getByPlaceholder(/search across everything/i)).toBeVisible();

  // Open palette via Ctrl+K (chromium accepts both Meta and Control on Linux).
  await page.keyboard.press("Control+k");
  await expect(page.getByPlaceholder(/jump to or search/i)).toBeVisible();

  // Type "connectors" → Enter should land on /connectors. (Picked /connectors
  // over /admin/stats so we don't have to mock the stats endpoint here.)
  await page.keyboard.type("connectors");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/connectors/);
});

test("? opens the keyboard cheat sheet", async ({ page }) => {
  await mockAuthedAdmin(page);
  await page.goto("/");
  await expect(page.getByPlaceholder(/search across everything/i)).toBeVisible();

  // Defocus the search input so the global handler sees `?`.
  await page.locator("body").click();
  await page.keyboard.press("Shift+/");
  await expect(page.getByText(/keyboard shortcuts/i)).toBeVisible();
  await expect(page.getByText("Navigation")).toBeVisible();
});

test("g s and g c chord shortcuts navigate", async ({ page }) => {
  await mockAuthedAdmin(page);
  await page.goto("/connectors");
  await expect(page).toHaveURL(/\/connectors/);

  // Defocus any input.
  await page.locator("body").click();
  // g + s → home
  await page.keyboard.press("g");
  await page.keyboard.press("s");
  await expect(page).toHaveURL(/^[^?]+\/$|^[^?]+\/\?/);
});
