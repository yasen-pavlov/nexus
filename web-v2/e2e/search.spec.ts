import { test, expect, type Page } from "@playwright/test";

// Intercept the authenticated API surface with stable fixtures so the test
// doesn't depend on live indexed content. The login endpoint still hits the
// real Go backend.

async function mockAuthed(page: Page) {
  // Stash a fake token so the front-end skips the login screen. The server
  // would reject it, but our mocked /api/auth/me accepts it.
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

  await page.route("**/api/connectors/", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: "c1",
            type: "imap",
            name: "personal-email",
            config: {},
            enabled: true,
            schedule: "",
            shared: false,
            status: "ok",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
            last_run: "2026-04-10T00:00:00Z",
            user_id: "u1",
          },
        ],
      }),
    }),
  );

  await page.route("**/api/documents/*/related", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { outgoing: [], incoming: [] } }),
    }),
  );
}

test("welcome state is shown with no query", async ({ page }) => {
  await mockAuthed(page);
  await page.goto("/");
  await expect(page.getByText("ready.")).toBeVisible();
  await expect(
    page.getByText("Search across everything"),
  ).toBeVisible();
});

test("typing a query fetches results and renders per-source cards", async ({
  page,
}) => {
  await mockAuthed(page);
  await page.route("**/api/search*", (route) => {
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          documents: [
            {
              id: "h1",
              source_type: "imap",
              source_name: "personal-email",
              source_id: "INBOX:1",
              title: "Invoice from Hospital",
              content: "Please find attached",
              metadata: {
                from: "Hospital <bill@hospital.com>",
                folder: "INBOX",
                has_attachments: true,
                attachment_filenames: ["invoice.pdf"],
              },
              visibility: "private",
              created_at: "2026-04-10T00:00:00Z",
              indexed_at: "2026-04-10T00:01:00Z",
              rank: 0.9,
              headline: "Please find <em>attached</em>",
            },
            {
              id: "h2",
              source_type: "telegram",
              source_name: "tg-main",
              source_id: "12345:100-120",
              title: "Family chat window",
              content: "Dinner at 7",
              metadata: {
                chat_name: "Family",
                chat_id: 12345,
                message_count: 21,
                anchor_message_id: 100,
              },
              conversation_id: "12345",
              visibility: "private",
              created_at: "2026-04-05T18:00:00Z",
              indexed_at: "2026-04-05T18:05:00Z",
              rank: 0.8,
              headline: "<em>Dinner</em> at 7",
            },
          ],
          total_count: 2,
          query: "test",
          facets: {
            source_type: [
              { value: "imap", count: 1 },
              { value: "telegram", count: 1 },
            ],
            source_name: [
              { value: "personal-email", count: 1 },
              { value: "tg-main", count: 1 },
            ],
          },
        },
      }),
    });
  });

  await page.goto("/");
  await page.getByRole("searchbox").fill("test");

  await expect(page.getByText("Invoice from Hospital")).toBeVisible({
    timeout: 2000,
  });
  await expect(page.getByText("Family chat window")).toBeVisible();
  await expect(page.getByRole("button", { name: /open in chat/i })).toBeVisible();
  await expect(page.getByText("invoice.pdf")).toBeVisible();
});

test("clicking a source facet updates the URL", async ({ page }) => {
  await mockAuthed(page);
  let lastSearchURL = "";
  await page.route("**/api/search*", (route) => {
    lastSearchURL = route.request().url();
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          documents: [
            {
              id: "h1",
              source_type: "imap",
              source_name: "personal-email",
              source_id: "a",
              title: "Mail 1",
              content: "",
              metadata: { from: "a@b.com" },
              visibility: "private",
              created_at: "2026-04-10T00:00:00Z",
              indexed_at: "2026-04-10T00:01:00Z",
              rank: 0.9,
            },
          ],
          total_count: 1,
          query: "test",
          facets: {
            source_type: [{ value: "imap", count: 1 }],
            source_name: [{ value: "personal-email", count: 1 }],
          },
        },
      }),
    });
  });

  await page.goto("/?q=test");
  await expect(page.getByText("Mail 1")).toBeVisible();

  // Filter bar: no filters yet → click "add" to open the popover, pick imap.
  await page.getByRole("button", { name: "add" }).click();
  await page.getByRole("dialog").getByText("imap", { exact: true }).click();

  // TanStack Router JSON-encodes arrays: sources=["imap"] → sources=%5B%22imap%22%5D
  await expect(page).toHaveURL(/sources=/);
  await expect(page).toHaveURL(/imap/);
  // The fetch layer still sends comma-separated to the backend
  await expect(() => expect(lastSearchURL).toContain("sources=imap")).toPass();
});

test("open-in-chat navigates to the conversation placeholder", async ({
  page,
}) => {
  await mockAuthed(page);
  await page.route("**/api/search*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          documents: [
            {
              id: "h1",
              source_type: "telegram",
              source_name: "tg-main",
              source_id: "12345:100",
              title: "Family chat",
              content: "",
              metadata: {
                chat_name: "Family",
                anchor_message_id: 100,
              },
              conversation_id: "12345",
              visibility: "private",
              created_at: "2026-04-05T18:00:00Z",
              indexed_at: "2026-04-05T18:05:00Z",
              rank: 0.9,
            },
          ],
          total_count: 1,
          query: "hi",
          facets: {},
        },
      }),
    }),
  );

  await page.goto("/?q=hi");
  await page.getByRole("button", { name: /open in chat/i }).click();
  await expect(page).toHaveURL(/\/conversations\/telegram\/12345/);
});
