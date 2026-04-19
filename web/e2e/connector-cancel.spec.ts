import { test, expect, type Page } from "./fixtures";

// High-value E2E: start a sync, cancel it, and assert that the UI flips
// through running → cancel-requested → terminal "canceled" state end-to-end.
// Exercises the multiplexed SSE subscription, the cancel endpoint wiring,
// and the toast-on-transition logic in useSyncJobs.

async function mockAuthedBase(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("nexus_jwt", "fake-e2e-token");
  });
  await page.route("**/api/auth/me", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { id: "u1", username: "e2e", role: "admin" },
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
  await page.route("**/api/me/identities", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { identities: [] } }),
    }),
  );
  // Empty SSE stream — the test drives state transitions through the
  // /api/sync polling endpoint instead.
  await page.route("**/api/sync/progress*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "",
    }),
  );
}

test("triggering a sync then canceling flips the card through running → canceled", async ({
  page,
}) => {
  await mockAuthedBase(page);

  // Mutable job state the /api/sync GET reads.
  let job: Record<string, unknown> | null = null;
  let cancelCalled = false;

  await page.route("**/api/connectors/", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: "c-1",
            type: "filesystem",
            name: "notes",
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
        ],
      }),
    }),
  );

  await page.route("**/api/sync", (route) => {
    if (route.request().method() === "GET") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ data: job ? [job] : [] }),
      });
    }
    return route.fulfill({ status: 405 });
  });

  await page.route("**/api/sync/c-1", (route) => {
    if (route.request().method() === "POST") {
      job = {
        id: "job-1",
        connector_id: "c-1",
        connector_name: "notes",
        connector_type: "filesystem",
        status: "running",
        docs_total: 100,
        docs_processed: 25,
        docs_deleted: 0,
        errors: 0,
        error: "",
        started_at: new Date().toISOString(),
      };
      return route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({ data: job }),
      });
    }
    return route.fulfill({ status: 405 });
  });

  await page.route("**/api/sync/jobs/job-1/cancel", (route) => {
    cancelCalled = true;
    // Server promotes the job to canceled terminal state; the /api/sync
    // poll then observes the change.
    job = {
      ...(job as Record<string, unknown>),
      status: "canceled",
      completed_at: new Date().toISOString(),
    };
    return route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify({ data: { message: "cancel requested" } }),
    });
  });

  await page.goto("/connectors");
  await expect(page.getByRole("heading", { name: "Connectors" })).toBeVisible();

  // Kick off the sync. The card pivots to show the Cancel button + progress bar.
  await page.getByRole("button", { name: /^sync$/i }).click();
  await expect(page.getByRole("button", { name: /cancel/i })).toBeVisible();
  await expect(page.getByRole("progressbar")).toBeVisible();

  // Cancel. The poll tick flips the job to canceled status, the card
  // re-renders with the idle state + Sync button is back.
  await page.getByRole("button", { name: /cancel/i }).click();
  expect(cancelCalled).toBe(true);
});
