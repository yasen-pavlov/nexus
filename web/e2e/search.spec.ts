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

  // AppShell's useSyncJobs polls /api/sync and opens an SSE stream; without
  // stubs the real backend at :8080 returns 401 and drops the session.
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
}

test("welcome state is shown with no query", async ({ page }) => {
  await mockAuthed(page);
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Search across everything" }),
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

  // Filter bar shows source chips inline. "imap" renders as "Email" via
  // the SourceChip label mapping; click the pill directly.
  await page.getByRole("button", { pressed: false, name: /Email/ }).click();

  // TanStack Router JSON-encodes arrays: sources=["imap"] → sources=%5B%22imap%22%5D
  await expect(page).toHaveURL(/sources=/);
  await expect(page).toHaveURL(/imap/);
  // The fetch layer still sends comma-separated to the backend
  await expect(() => expect(lastSearchURL).toContain("sources=imap")).toPass();
});

test("open-in-chat opens the conversation at the anchor message", async ({
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
              source_id: "12345:100-120",
              title: "Family chat",
              content: "",
              metadata: {
                chat_name: "Family",
                anchor_message_id: 100,
                anchor_created_at: "2026-04-05T18:00:00Z",
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

  await page.route("**/api/me/identities", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          identities: [
            {
              connector_id: "c-tg",
              source_type: "telegram",
              source_name: "tg-main",
              external_id: "9001",
              external_name: "Me",
              has_avatar: false,
            },
          ],
        },
      }),
    }),
  );

  await page.route(
    "**/api/conversations/telegram/12345/messages*",
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            messages: [
              {
                id: "m1",
                source_type: "telegram",
                source_name: "tg-main",
                source_id: "12345:99:msg",
                title: "Family",
                content: "earlier message",
                metadata: {
                  chat_id: "12345",
                  chat_name: "Family",
                  message_id: 99,
                  sender_id: 1001,
                  sender_name: "Alice",
                },
                relations: [],
                conversation_id: "12345",
                hidden: true,
                visibility: "private",
                created_at: "2026-04-05T17:59:00Z",
                indexed_at: "2026-04-05T18:05:00Z",
              },
              {
                id: "m2",
                source_type: "telegram",
                source_name: "tg-main",
                source_id: "12345:100:msg",
                title: "Family",
                content: "dinner at 7",
                metadata: {
                  chat_id: "12345",
                  chat_name: "Family",
                  message_id: 100,
                  sender_id: 1001,
                  sender_name: "Alice",
                },
                relations: [],
                conversation_id: "12345",
                hidden: true,
                visibility: "private",
                created_at: "2026-04-05T18:00:00Z",
                indexed_at: "2026-04-05T18:05:00Z",
              },
            ],
          },
        }),
      }),
  );

  await page.goto("/?q=hi");
  await page.getByRole("button", { name: /open in chat/i }).click();
  await expect(page).toHaveURL(/\/conversations\/telegram\/12345/);
  await expect(page).toHaveURL(/anchor_id=100/);

  // The anchor message and at least one surrounding message are visible.
  await expect(page.getByText("dinner at 7")).toBeVisible();
  await expect(page.getByText("earlier message")).toBeVisible();

  // Anchor highlight: the article with id msg-12345:100:msg sits inside
  // the anchor wrapper which applies ring classes on mount.
  const anchor = page.locator("#msg-12345\\:100\\:msg");
  await expect(anchor).toBeVisible();
});

test("clicking an inline image in the chat opens a lightbox dismissed by Escape", async ({
  page,
}) => {
  await mockAuthed(page);

  await page.route("**/api/me/identities", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          identities: [
            {
              connector_id: "c-tg",
              source_type: "telegram",
              source_name: "tg-main",
              external_id: "9001",
              external_name: "Me",
              has_avatar: false,
            },
          ],
        },
      }),
    }),
  );

  // One message in the conversation carrying an image attachment.
  await page.route(
    "**/api/conversations/telegram/12345/messages*",
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            messages: [
              {
                id: "m1",
                source_type: "telegram",
                source_name: "tg-main",
                source_id: "12345:100:msg",
                title: "Family",
                content: "check this",
                metadata: {
                  chat_id: "12345",
                  chat_name: "Family",
                  message_id: 100,
                  sender_id: 1001,
                  sender_name: "Alice",
                  attachments: [
                    {
                      id: "d-img-1",
                      source_id: "12345:100:media",
                      filename: "photo.jpg",
                      mime_type: "image/jpeg",
                      size: 2048,
                    },
                  ],
                },
                relations: [],
                conversation_id: "12345",
                hidden: true,
                visibility: "private",
                created_at: "2026-04-05T18:00:00Z",
                indexed_at: "2026-04-05T18:05:00Z",
              },
            ],
          },
        }),
      }),
  );

  // 1x1 transparent PNG so the <img> actually has an image to render.
  const pngBase64 =
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=";
  const pngBytes = Buffer.from(pngBase64, "base64");
  await page.route("**/api/documents/d-img-1/content", (route) =>
    route.fulfill({
      status: 200,
      contentType: "image/png",
      body: pngBytes,
    }),
  );

  await page.goto("/conversations/telegram/12345");

  // Inline image is rendered inside the message bubble.
  const thumb = page.getByAltText("photo.jpg").first();
  await expect(thumb).toBeVisible();

  // No dialog before click.
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await thumb.click();

  // Lightbox appears.
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();

  // Body scroll is locked while the lightbox is open.
  const bodyOverflow = await page.evaluate(() => document.body.style.overflow);
  expect(bodyOverflow).toBe("hidden");

  // Escape closes.
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  // Body overflow restored.
  const restored = await page.evaluate(() => document.body.style.overflow);
  expect(restored).not.toBe("hidden");
});
