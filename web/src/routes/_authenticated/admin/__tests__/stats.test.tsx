import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { http, HttpResponse } from "msw";

import { routeTree } from "@/routeTree.gen";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { fakeToken } from "@/test/mocks/handlers";

const originalMatchMedia = window.matchMedia;

function mockMobile(mobile: boolean) {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: mobile,
    media: "",
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }) as unknown as typeof window.matchMedia;
}

function setup() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: ["/admin/stats"] }),
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

beforeEach(() => {
  setToken(fakeToken);
});

afterEach(() => {
  server.resetHandlers();
  window.matchMedia = originalMatchMedia;
});

const populatedStats = {
  total_documents: 1234,
  total_chunks: 5678,
  users_count: 3,
  per_source: [
    {
      source_type: "filesystem",
      source_name: "notes",
      document_count: 500,
      chunk_count: 1000,
      cache_count: 20,
      cache_bytes: 10 * 1024 * 1024,
      latest_indexed_at: "2026-04-18T00:00:00Z",
    },
    {
      source_type: "telegram",
      source_name: "chats",
      document_count: 734,
      chunk_count: 4678,
      cache_count: 44,
      cache_bytes: 24 * 1024 * 1024,
      latest_indexed_at: "2026-04-17T00:00:00Z",
    },
  ],
  embedding: {
    enabled: true,
    provider: "voyage",
    model: "voyage-4-large",
    dimension: 1024,
  },
  rerank: {
    enabled: true,
    provider: "voyage",
    model: "rerank-2",
  },
};

describe("admin/stats", () => {
  it("renders KPI totals and the per-source table on desktop", async () => {
    mockMobile(false);
    server.use(
      http.get("*/api/admin/stats", () =>
        HttpResponse.json({ data: populatedStats }),
      ),
    );
    setup();
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: /Stats/i, level: 1 }),
      ).toBeInTheDocument(),
    );
    // Both sources appear in the desktop table.
    await waitFor(() => expect(screen.getByText("notes")).toBeInTheDocument());
    expect(screen.getByText("chats")).toBeInTheDocument();
    // The populated KPI count shows up somewhere as a formatted number.
    // `formatCount` uses the locale separator, so match flexibly.
    expect(
      screen.getByText(/1[,.\s\u202f]234/),
    ).toBeInTheDocument();
  });

  it("renders the mobile card list when useIsMobile() is true", async () => {
    mockMobile(true);
    server.use(
      http.get("*/api/admin/stats", () =>
        HttpResponse.json({ data: populatedStats }),
      ),
    );
    setup();
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: /Stats/i, level: 1 }),
      ).toBeInTheDocument(),
    );
    // The mobile list still shows source rows.
    await waitFor(() => expect(screen.getByText("notes")).toBeInTheDocument());
  });

  it("shows the empty-state when per_source is empty", async () => {
    mockMobile(false);
    server.use(
      http.get("*/api/admin/stats", () =>
        HttpResponse.json({
          data: { ...populatedStats, per_source: [], total_documents: 0 },
        }),
      ),
    );
    setup();
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: /Stats/i, level: 1 }),
      ).toBeInTheDocument(),
    );
    // KpiPlaque "Latest activity" falls back to "No content indexed yet"
    // when per_source is empty — that caption is unique to the empty path.
    await waitFor(() =>
      expect(screen.getByText(/No content indexed yet/)).toBeInTheDocument(),
    );
  });

  it("renders the error plaque when the stats request fails", async () => {
    mockMobile(false);
    server.use(
      http.get("*/api/admin/stats", () =>
        HttpResponse.json({ error: "internal" }, { status: 500 }),
      ),
    );
    setup();
    await waitFor(() =>
      expect(screen.getByText(/internal/i)).toBeInTheDocument(),
    );
  });
});
