import { test, expect, type Page } from "@playwright/test";

async function mockAuthed(page: Page) {
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
          username: "alice",
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
  // useUsers() runs unconditionally inside ChangePasswordSheet — need to
  // mock the list endpoint or opening the sheet 401s and bounces to /login.
  await page.route("**/api/users", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );
}

test("Account: visiting /account shows identity and change-password trigger", async ({
  page,
}) => {
  await mockAuthed(page);
  await page.goto("/account");

  await expect(
    page.getByRole("heading", { name: /^account$/i }),
  ).toBeVisible();
  // username visible somewhere on the page
  await expect(page.getByText("alice").first()).toBeVisible();
  await expect(page.getByText(/admin/i).first()).toBeVisible();

  await expect(
    page.getByRole("button", { name: /change…/i }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: /sign out/i }),
  ).toBeVisible();
});

test("Account: clicking Change opens the password sheet", async ({ page }) => {
  await mockAuthed(page);
  await page.goto("/account");

  await page.getByRole("button", { name: /change…/i }).click();
  await expect(page.getByText(/set a new password/i)).toBeVisible();
});
