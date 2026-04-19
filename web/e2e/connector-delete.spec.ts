import { test, expect, type Page } from "./fixtures";

// High-value E2E: the typed-confirmation delete dialog. Destroying a
// connector is irreversible (MTProto sessions, cached binaries, indexed
// chunks all go); this test locks the UX that prevents one-click delete.

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

test("the typed-confirmation dialog blocks delete until the name matches", async ({
  page,
}) => {
  await mockAuthedBase(page);

  let connectors: Array<Record<string, unknown>> = [
    {
      id: "c-zap",
      type: "imap",
      name: "personal-email",
      config: {},
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
    },
  ];

  let deleteCalled = false;
  await page.route("**/api/connectors/", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: connectors }),
    }),
  );
  await page.route("**/api/connectors/c-zap", (route) => {
    if (route.request().method() === "DELETE") {
      deleteCalled = true;
      connectors = [];
      return route.fulfill({ status: 204 });
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: connectors[0] }),
    });
  });

  await page.goto("/connectors");
  await expect(page.getByText("personal-email").first()).toBeVisible();

  // Open the row's menu and click "Remove connector…"
  await page.getByRole("button", { name: /more actions/i }).click();
  await page.getByRole("menuitem", { name: /remove connector/i }).click();

  // Dialog is open; Remove button starts disabled.
  const confirm = page.getByRole("button", { name: /remove personal-email/i });
  await expect(confirm).toBeVisible();
  await expect(confirm).toBeDisabled();

  // Wrong name first — still disabled.
  const input = page.getByPlaceholder("personal-email");
  await input.fill("personal-emai");
  await expect(confirm).toBeDisabled();

  // Complete the name → button arms. Click to fire the delete.
  await input.fill("personal-email");
  await expect(confirm).toBeEnabled();
  await confirm.click();

  expect(deleteCalled).toBe(true);
});
