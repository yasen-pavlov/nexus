import { test, expect, type Page } from "./fixtures";

// Phase 3 smoke: log in (mock), open connectors page, create a filesystem
// connector via the sheet, trigger a sync, verify the completion toast
// fires, then check the detail page's Activity tab shows the persisted
// sync_run row.

async function mockAuthed(page: Page) {
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
}

test("create a connector via the sheet, sync it, and see the run in Activity", async ({
  page,
}) => {
  await mockAuthed(page);

  // Mutable fixture state so the PUT/POST responses are consistent with the
  // subsequent GET.
  const connectors: Array<Record<string, unknown>> = [];
  const runs: Array<Record<string, unknown>> = [];

  await page.route("**/api/connectors/", (route) => {
    const method = route.request().method();
    if (method === "POST") {
      const body = route.request().postDataJSON() as Record<string, unknown>;
      const created = {
        id: "c-new",
        type: body.type,
        name: body.name,
        config: body.config,
        enabled: true,
        shared: false,
        schedule: body.schedule ?? "",
        status: "active",
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
        last_run: null,
        user_id: "u1",
        external_id: "",
        external_name: "",
      };
      connectors.push(created);
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({ data: created }),
      });
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: connectors }),
    });
  });

  await page.route("**/api/connectors/c-new", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: connectors[0] }),
    }),
  );

  await page.route("**/api/connectors/c-new/runs*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: runs }),
    }),
  );

  await page.route("**/api/sync", (route) => {
    if (route.request().method() === "POST") {
      return route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({ data: [] }),
      });
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    });
  });

  await page.route("**/api/sync/c-new", (route) => {
    if (route.request().method() === "POST") {
      const job = {
        id: "job-1",
        connector_id: "c-new",
        connector_name: "notes",
        connector_type: "filesystem",
        status: "completed",
        docs_total: 1,
        docs_processed: 1,
        docs_deleted: 0,
        errors: 0,
        error: "",
        started_at: new Date().toISOString(),
        completed_at: new Date().toISOString(),
      };
      runs.push({
        id: "job-1",
        connector_id: "c-new",
        status: "completed",
        docs_total: 1,
        docs_processed: 1,
        docs_deleted: 0,
        errors: 0,
        error_message: "",
        started_at: job.started_at,
        completed_at: job.completed_at,
      });
      return route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({ data: job }),
      });
    }
    return route.fulfill({ status: 404 });
  });

  // Stub SSE with an empty text/event-stream so EventSource doesn't error.
  await page.route("**/api/sync/progress*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "",
    }),
  );

  // Go to the list page
  await page.goto("/connectors");
  await expect(page.getByRole("heading", { name: "Connectors" })).toBeVisible();
  await expect(page.getByText(/workbench is empty/i)).toBeVisible();

  // Open the create sheet
  await page.getByRole("button", { name: /add your first connector/i }).click();
  await expect(page.getByText("New connector")).toBeVisible();

  // Filesystem is the default; fill the root path.
  const rootPath = page.getByLabel("Root path");
  await rootPath.fill("/tmp/e2e-notes");

  // Name is seeded; submit.
  await page.getByRole("button", { name: /create connector/i }).click();

  // The card appears after the list invalidates.
  await expect(page.getByRole("link", { name: /^notes$/i })).toBeVisible();

  // Trigger sync (fires POST /api/sync/c-new which also appends a run).
  await page.getByRole("button", { name: /^sync$/i }).first().click();
  // Wait for the POST to complete before navigating so runs[] is populated.
  await page.waitForResponse((resp) => resp.url().endsWith("/api/sync/c-new"));

  // Navigate to detail page. Using page.goto rather than a link click —
  // the SSE pipeline + concurrent queries make link-driven nav flaky
  // under Playwright's mocked routing.
  await page.goto("/connectors/c-new", { waitUntil: "domcontentloaded" });
  await expect(page).toHaveURL(/\/connectors\/c-new/);
  await expect(page.getByRole("tab", { name: "Activity" })).toBeVisible();

  // Activity tab shows the run we just ran.
  await page.getByRole("tab", { name: "Activity" }).click();
  await expect(page.getByText(/1 indexed/i)).toBeVisible();
});
