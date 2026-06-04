import { test, expect } from "./fixtures";

// The login screen calls GET /api/health on mount to decide login vs. first-run
// register. With no backend, that proxies to a dead :8080 and the query retries
// for ~7s while the form shows "Loading…", blowing the 5s assertions. Mock it
// (and the unauthenticated /api/auth/me) so the form renders deterministically.
test.beforeEach(async ({ page }) => {
  await page.route("**/api/health", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { status: "ok", setup_required: false } }),
    }),
  );
  await page.route("**/api/auth/me", (route) =>
    route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({ error: "Unauthorized" }),
    }),
  );
});

test("redirects to login when not authenticated", async ({ page }) => {
  await page.goto("/");
  // Should redirect to /login
  await expect(page).toHaveURL(/\/login/);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
});

test("shows login form fields", async ({ page }) => {
  await page.goto("/login");
  await expect(page.getByLabel("Username")).toBeVisible();
  await expect(page.getByLabel("Password")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
});
