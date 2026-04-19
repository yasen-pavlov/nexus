import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, waitFor, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";

import { routeTree } from "@/routeTree.gen";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { fakeToken, fakeUserToken } from "@/test/mocks/handlers";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

function filesystemConnector(overrides = {}) {
  return {
    id: "c1",
    type: "filesystem",
    name: "notes",
    config: { root_path: "/tmp/notes", patterns: "*.md" },
    enabled: true,
    schedule: "",
    shared: false,
    status: "ok",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    last_run: "2026-04-10T00:00:00Z",
    user_id: "u1",
    external_id: "",
    external_name: "",
    ...overrides,
  };
}

function telegramConnector(overrides = {}) {
  return filesystemConnector({
    id: "c-tg",
    type: "telegram",
    name: "tg",
    config: {},
    ...overrides,
  });
}

function mockDetail(connector = filesystemConnector()) {
  server.use(
    http.get(`*/api/connectors/${connector.id}`, () =>
      HttpResponse.json({ data: connector }),
    ),
    http.get(`*/api/connectors/${connector.id}/runs`, () =>
      HttpResponse.json({ data: [] }),
    ),
  );
}

function setup(initialPath: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [initialPath] }),
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
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  vi.mocked(toast.info).mockClear();
});
afterEach(() => server.resetHandlers());

describe("connectors/:id route", () => {
  it("renders the connector header with name + type label", async () => {
    mockDetail();
    setup("/connectors/c1");
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "notes" })).toBeInTheDocument(),
    );
    // Back link to the connectors list.
    expect(screen.getByRole("link", { name: /all connectors/i })).toHaveAttribute(
      "href",
      "/connectors",
    );
  });

  it("Sync now triggers POST /api/sync/:id", async () => {
    mockDetail();
    let hit = false;
    server.use(
      http.post("*/api/sync/c1", () => {
        hit = true;
        return HttpResponse.json({
          data: {
            id: "job-1",
            connector_id: "c1",
            connector_name: "notes",
            connector_type: "filesystem",
            status: "running",
            docs_total: 0,
            docs_processed: 0,
            docs_deleted: 0,
            errors: 0,
            error: "",
            started_at: "2026-04-19T00:00:00Z",
            completed_at: "",
          },
        });
      }),
    );
    setup("/connectors/c1");
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "notes" })).toBeInTheDocument(),
    );
    await userEvent.click(screen.getByRole("button", { name: /sync now/i }));
    await waitFor(() => expect(hit).toBe(true));
  });

  it("renders the Identity tab only for telegram connectors", async () => {
    mockDetail(telegramConnector());
    setup("/connectors/c-tg");
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "tg" })).toBeInTheDocument(),
    );
    expect(screen.getByRole("tab", { name: /identity/i })).toBeInTheDocument();
  });

  it("filesystem connectors do not get an Identity tab", async () => {
    mockDetail();
    setup("/connectors/c1");
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "notes" })).toBeInTheDocument(),
    );
    expect(
      screen.queryByRole("tab", { name: /identity/i }),
    ).not.toBeInTheDocument();
  });

  it("shared connector disables Sync for non-admin users", async () => {
    setToken(fakeUserToken);
    mockDetail(filesystemConnector({ shared: true, user_id: "u1" }));
    setup("/connectors/c1");
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "notes" })).toBeInTheDocument(),
    );
    const sync = screen.getByRole("button", { name: /sync now/i });
    expect(sync).toBeDisabled();
  });

  it("shows skeleton placeholders while the detail endpoint is pending", () => {
    // Keep the handlers unresolved by not calling `mockDetail()` — the
    // default handler returns 404 for this id but the query stays
    // isLoading until the fetch settles, which is long enough for the
    // first commit to render the skeleton branch.
    server.use(
      http.get("*/api/connectors/c1", async () => {
        await new Promise((r) => setTimeout(r, 5000));
        return HttpResponse.json({ data: filesystemConnector() });
      }),
      http.get("*/api/connectors/c1/runs", () =>
        HttpResponse.json({ data: [] }),
      ),
    );
    setup("/connectors/c1");
    expect(
      screen.queryByRole("heading", { name: "notes" }),
    ).not.toBeInTheDocument();
  });
});
