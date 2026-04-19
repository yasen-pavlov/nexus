import { test, expect, type Page } from "@playwright/test";

// High-value E2E: walk the three admin surfaces end-to-end — Settings
// (read + dirty state), Users (create + delete via typed confirm), Stats
// (KPI strip renders with real numbers). Mocks the whole BE so the test
// stays hermetic + fast; the real BE gets verified in the live-verify
// step documented in the Phase 4 plan.

async function mockAuthedBase(page: Page) {
  // TanStack Router + Query dev toolbars render fixed-position buttons at
  // bottom-right. Sheets in this app also pin their primary CTA bottom-right,
  // so the devtools steal clicks. Hide them for the whole test via an
  // inject-once style tag.
  await page.addInitScript(() => {
    localStorage.setItem("nexus_jwt", "fake-e2e-token");
    const style = document.createElement("style");
    style.textContent = `
      button[aria-label="Open Tanstack query devtools"],
      button[aria-label="Open TanStack Router Devtools"] {
        display: none !important;
      }
    `;
    // Document might not have <head> yet on very early navigation hooks.
    const mount = () => (document.head || document.documentElement).appendChild(style);
    if (document.head) mount();
    else document.addEventListener("DOMContentLoaded", mount);
  });
  await page.route("**/api/auth/me", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { id: "u1", username: "admin", role: "admin" },
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
  await page.route("**/api/sync/progress*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "",
    }),
  );
  await page.route("**/api/sync", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );
  await page.route("**/api/connectors/", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );
}

test("admin visits Stats: KPI numbers render, engine panel reads provider+model", async ({
  page,
}) => {
  await mockAuthedBase(page);

  await page.route("**/api/admin/stats", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          total_documents: 42,
          total_chunks: 1337,
          users_count: 2,
          per_source: [
            {
              source_type: "imap",
              source_name: "icloud",
              document_count: 30,
              chunk_count: 900,
              latest_indexed_at: new Date(Date.now() - 5 * 60_000).toISOString(),
              cache_count: 3,
              cache_bytes: 2048,
            },
            {
              source_type: "telegram",
              source_name: "notes",
              document_count: 12,
              chunk_count: 437,
              latest_indexed_at: new Date(
                Date.now() - 2 * 3600 * 1000,
              ).toISOString(),
              cache_count: 8,
              cache_bytes: 524_288,
            },
          ],
          embedding: {
            enabled: true,
            provider: "voyage",
            model: "voyage-3-large",
            dimension: 1024,
          },
          rerank: {
            enabled: false,
            provider: "",
            model: "",
          },
        },
      }),
    }),
  );

  await page.goto("/admin/stats");

  // KPI plaques — big numeric values
  await expect(page.getByText("42", { exact: true })).toBeVisible();
  await expect(page.getByText("2", { exact: true })).toBeVisible(); // sources count

  // Per-source rows pick up both source types
  await expect(page.getByText("icloud")).toBeVisible();
  await expect(page.getByText("notes")).toBeVisible();

  // Engine card reads the hot-loaded provider
  await expect(page.getByText("voyage-3-large")).toBeVisible();
  await expect(page.getByText("1024d")).toBeVisible();
  // Rerank shown as Disabled with Configure CTA
  await expect(
    page.getByRole("link", { name: /Configure →/ }),
  ).toBeVisible();
});

test("admin creates a new user via the sheet and the row lands in the table", async ({
  page,
}) => {
  await mockAuthedBase(page);

  let users = [
    {
      id: "u1",
      username: "admin",
      role: "admin",
      created_at: "2026-01-01T00:00:00Z",
    },
  ];
  await page.route("**/api/users", (route) => {
    if (route.request().method() === "GET") {
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ data: users }),
      });
    } else if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as {
        username: string;
        role: "admin" | "user";
      };
      const created = {
        id: "u2",
        username: body.username,
        role: body.role,
        created_at: new Date().toISOString(),
      };
      users = [...users, created];
      route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({ data: created }),
      });
    }
  });

  await page.goto("/admin/users");

  await expect(page.getByRole("heading", { name: "Users" })).toBeVisible();
  await expect(page.getByText("admin").first()).toBeVisible();

  await page.getByRole("button", { name: /New user/i }).click();
  await page.getByPlaceholder("alice").fill("bob");
  await page.getByPlaceholder("min 8 characters").fill("hunter22-hunter22");
  await page.getByRole("button", { name: /^Create user$/ }).click();

  // The new row surfaces after the list query invalidation.
  await expect(page.getByText("bob")).toBeVisible();
  await expect(page.getByRole("button", { name: "Actions for bob" })).toBeVisible();
});

test("admin visits Settings: Embeddings section renders with current provider", async ({
  page,
}) => {
  await mockAuthedBase(page);

  await page.route("**/api/settings/embedding", (route) => {
    if (route.request().method() === "GET") {
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            provider: "voyage",
            model: "voyage-3-large",
            api_key: "****abcd",
            ollama_url: "http://localhost:11434",
          },
        }),
      });
    }
  });
  await page.route("**/api/settings/rerank", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { provider: "", model: "", api_key: "" },
      }),
    }),
  );
  await page.route("**/api/settings/retention", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          retention_days: 90,
          retention_per_connector: 200,
          sweep_interval_minutes: 60,
          min_sweep_interval_minutes: 5,
        },
      }),
    }),
  );
  await page.route("**/api/settings/ranking", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          source_half_life_days: { telegram: 14, imap: 30, filesystem: 90, paperless: 180 },
          source_recency_floor: { telegram: 0.65, imap: 0.75, filesystem: 0.85, paperless: 0.9 },
          source_trust_weight: { telegram: 0.92, imap: 0.92, filesystem: 1, paperless: 1.05 },
          reranker_min_score: 0.4,
          metadata_bonus_enabled: true,
          source_trust_enabled: true,
          known_source_types: ["imap", "telegram", "paperless", "filesystem"],
          runtime_wiring_active: false,
        },
      }),
    }),
  );
  await page.route("**/api/storage/stats", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );
  await page.route("**/api/admin/stats", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          total_documents: 0,
          total_chunks: 0,
          users_count: 1,
          per_source: [],
          embedding: {
            enabled: true,
            provider: "voyage",
            model: "voyage-3-large",
            dimension: 1024,
          },
          rerank: { enabled: false, provider: "", model: "" },
        },
      }),
    }),
  );

  await page.goto("/admin/settings");

  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();

  // Left-nav ordinals render as a reading-room table of contents.
  // Scope to <aside> so Phase 5's mobile TOC trigger (md:hidden) doesn't
  // shadow the assertion via getByText.first().
  const desktopNav = page.locator("aside");
  await expect(desktopNav.getByText("Embeddings", { exact: true })).toBeVisible();
  await expect(desktopNav.getByText("01")).toBeVisible();

  // Masked key shown + Replace button surfaced
  await expect(page.getByText("abcd")).toBeVisible();
  await expect(page.getByRole("button", { name: /Replace/ })).toBeVisible();

  // Ranking section renders with the live form now (not a read-only preview).
  await expect(
    page.getByText("Apply source trust weights").first(),
  ).toBeVisible();
});

test("admin tunes the Telegram ranking preset and the save round-trips", async ({
  page,
}) => {
  await mockAuthedBase(page);

  const defaults = {
    source_half_life_days: { telegram: 14, imap: 30, filesystem: 90, paperless: 180 },
    source_recency_floor: { telegram: 0.65, imap: 0.75, filesystem: 0.85, paperless: 0.9 },
    source_trust_weight: { telegram: 0.92, imap: 0.92, filesystem: 1, paperless: 1.05 },
    metadata_bonus_enabled: true,
    source_trust_enabled: true,
    known_source_types: ["imap", "telegram", "paperless", "filesystem"],
  };

  // Mock the other Settings endpoints the page also queries on mount, so
  // the ranking section's paint isn't blocked by unrelated pending fetches.
  await page.route("**/api/settings/embedding", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { provider: "", model: "", api_key: "", ollama_url: "" },
      }),
    }),
  );
  await page.route("**/api/settings/rerank", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { provider: "", model: "", api_key: "", min_score: 0.4 },
      }),
    }),
  );
  await page.route("**/api/settings/retention", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          retention_days: 90,
          retention_per_connector: 200,
          sweep_interval_minutes: 60,
          min_sweep_interval_minutes: 5,
        },
      }),
    }),
  );
  await page.route("**/api/storage/stats", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [] }),
    }),
  );
  await page.route("**/api/admin/stats", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          total_documents: 0,
          total_chunks: 0,
          users_count: 1,
          per_source: [],
          embedding: { enabled: false, provider: "", model: "" },
          rerank: { enabled: false, provider: "", model: "" },
        },
      }),
    }),
  );

  // Ranking endpoint: GET returns defaults, PUT captures the submitted body
  // and echoes it back so the test can assert round-trip.
  let capturedPut: Record<string, unknown> | null = null;
  await page.route("**/api/settings/ranking", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ data: defaults }),
      });
    } else if (route.request().method() === "PUT") {
      capturedPut = route.request().postDataJSON();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: { ...defaults, ...capturedPut },
        }),
      });
    }
  });

  await page.goto("/admin/settings#ranking");

  // The ranking section eventually renders after its own query resolves.
  await expect(
    page.getByText("Apply source trust weights").first(),
  ).toBeVisible();

  // Locate the Telegram card via the tonal-hue style the component applies.
  const telegramCard = page.locator(
    'article[style*="--source-telegram"]',
  );
  await expect(telegramCard).toBeVisible();

  // Click Telegram's "Archive" preset chip.
  await telegramCard.getByRole("button", { name: "Archive" }).click();

  // Plain-language readout should pick up Archive's knobs: 60-day
  // half-life (= 2 months), 90% floor, +8% trust (1.0 weight vs 0.92
  // default → neutral label).
  await expect(telegramCard).toContainText("Half-relevance after 2 months");
  await expect(telegramCard).toContainText("90%");

  // Draft bar surfaces since preset moved values off defaults.
  await expect(page.getByText(/Draft · not saved yet/)).toBeVisible();

  // Save; assert PUT fired with Archive values for telegram.
  await page.getByRole("button", { name: /Save changes/ }).click();
  await expect(page.getByText(/Draft · not saved yet/)).toBeHidden({
    timeout: 3000,
  });
  expect(capturedPut).not.toBeNull();
  const body = capturedPut as unknown as {
    source_half_life_days: Record<string, number>;
    source_recency_floor: Record<string, number>;
  };
  expect(body.source_half_life_days.telegram).toBe(60);
  expect(body.source_recency_floor.telegram).toBeCloseTo(0.9);
});
