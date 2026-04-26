import { test, expect, type Page } from "./fixtures";

// E2E for the Ask (RAG chat) surface. We stub all of /api/chats/* + the
// SSE messages endpoint so the test runs deterministically without a
// live LLM. The login form still hits the real backend; the Ask routes
// run entirely against mocks via the AppShell auth boundary.

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
        data: { id: "u1", username: "muty", role: "admin", created_at: "2026-01-01T00:00:00Z" },
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
    route.fulfill({ status: 200, contentType: "text/event-stream", body: "" }),
  );
  await page.route("**/api/sync/stats", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { connectors: 0, last_sync: null, running: 0, total_docs: 0 },
      }),
    }),
  );
  await page.route("**/api/llm/models", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: "anthropic:claude-sonnet-4-6",
            provider: "anthropic",
            bare_id: "claude-sonnet-4-6",
            display_name: "claude-sonnet-4-6",
            context_window: 200000,
            supports_citations: true,
            supports_tools: true,
            supports_vision: false,
            supports_caching: true,
            input_cost_per_mtok: 3,
            output_cost_per_mtok: 15,
            typical_ttft_ms: 800,
          },
        ],
      }),
    }),
  );
}

const SAMPLE_CHAT = {
  id: "chat-1",
  user_id: "u1",
  title: "",
  default_model: "anthropic:claude-sonnet-4-6",
  created_at: "2026-04-26T00:00:00Z",
  updated_at: "2026-04-26T00:00:00Z",
};

test("landing renders empty state and example pills", async ({ page }) => {
  await mockAuthed(page);
  await page.route("**/api/chats?**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { chats: [], total: 0 } }),
    }),
  );

  await page.goto("/ask");

  await expect(page.getByRole("heading", { name: /What can I help you find/ }))
    .toBeVisible();
  await expect(
    page.getByRole("button", { name: /Summarise the last week of Anthropic invoices/i }),
  ).toBeVisible();
  await expect(page.getByText(/No chats yet/)).toBeVisible();
});

test("recent-chats list renders previews", async ({ page }) => {
  await mockAuthed(page);
  await page.route("**/api/chats?**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          chats: [
            {
              ...SAMPLE_CHAT,
              first_message_preview: "What did Anthropic invoice me last month?",
            },
          ],
          total: 1,
        },
      }),
    }),
  );

  await page.goto("/ask");

  await expect(
    page.getByText(/What did Anthropic invoice me last month/),
  ).toBeVisible();
});

test("submitting on the landing creates a chat and navigates", async ({ page }) => {
  await mockAuthed(page);
  await page.route("**/api/chats?**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { chats: [], total: 0 } }),
    }),
  );
  await page.route("**/api/chats", (route) => {
    if (route.request().method() === "POST") {
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({ data: SAMPLE_CHAT }),
      });
    }
    return route.fallback();
  });
  await page.route("**/api/chats/chat-1", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { chat: SAMPLE_CHAT, messages: [] } }),
    }),
  );

  // Stubbed SSE for the orchestrator. Emit a deterministic frame
  // sequence so we can assert UI transitions.
  await page.route("**/api/chats/chat-1/messages", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: [
        `event: retrieving\ndata: {"query":"hello"}`,
        `event: evidence\ndata: {"chunks":[{"id":"d1","title":"Doc 1","source":"imap"}]}`,
        `event: text\ndata: {"delta":"Hello "}`,
        `event: text\ndata: {"delta":"world."}`,
        `event: citation\ndata: {"doc_id":"d1","cited_text":"hello","span":[0,5]}`,
        `event: usage\ndata: {"input":100,"output":50,"cache_read":0,"cache_write":0}`,
        `event: done\ndata: {"stop_reason":"end_turn","message_id":"m1"}`,
        ``, // trailing blank to flush the last frame
      ].join("\n\n"),
    }),
  );

  await page.goto("/ask");

  const composer = page.getByPlaceholder(/Ask anything/);
  await composer.fill("What is the answer?");
  // Cmd+Enter sends. Playwright maps Meta on macOS, Control elsewhere — both
  // work because the composer accepts either modifier.
  await composer.press("Control+Enter");

  // We routed to /ask/{id}?q=…
  await expect(page).toHaveURL(/\/ask\/chat-1/);
  // Evidence card appears.
  await expect(page.getByText("Doc 1")).toBeVisible({ timeout: 10_000 });
  // Streamed answer text appears. Citation pill splits the text across
  // sibling <span>s — match the prefix and suffix independently.
  await expect(page.getByText(/Hello/)).toBeVisible();
  await expect(page.getByText(/world\./)).toBeVisible();
  // Citation pill rendered with number 1.
  await expect(page.getByRole("button", { name: /Citation 1/ }).first())
    .toBeVisible();
});

test("skipped_retrieval hides the evidence rail and shows the muted phase chip", async ({ page }) => {
  await mockAuthed(page);
  await page.route("**/api/chats?**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { chats: [], total: 0 } }),
    }),
  );
  await page.route("**/api/chats", (route) => {
    if (route.request().method() === "POST") {
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({ data: SAMPLE_CHAT }),
      });
    }
    return route.fallback();
  });
  await page.route("**/api/chats/chat-1", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { chat: SAMPLE_CHAT, messages: [] } }),
    }),
  );
  await page.route("**/api/chats/chat-1/messages", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: [
        // No retrieving / evidence frames — the rewriter judged that
        // history alone could answer this turn.
        `event: skipped_retrieval\ndata: {"query":"thanks"}`,
        `event: text\ndata: {"delta":"You're welcome."}`,
        `event: usage\ndata: {"input":12,"output":4,"cache_read":0,"cache_write":0}`,
        `event: done\ndata: {"stop_reason":"end_turn","message_id":"m1"}`,
        ``,
      ].join("\n\n"),
    }),
  );

  await page.goto("/ask");
  const composer = page.getByPlaceholder(/Ask anything/);
  await composer.fill("thanks!");
  await composer.press("Control+Enter");

  await expect(page).toHaveURL(/\/ask\/chat-1/);
  // The muted "Answering from history" chip appears.
  await expect(page.getByText("Answering from history")).toBeVisible({
    timeout: 10_000,
  });
  // Answer body is reachable.
  await expect(page.getByText("You're welcome.")).toBeVisible();
});
