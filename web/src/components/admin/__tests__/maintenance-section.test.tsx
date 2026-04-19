import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";

import { render, screen, userEvent, waitFor } from "@/test/test-utils";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { MaintenanceSection } from "../maintenance-section";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

function mockSystemStats(
  overrides: Partial<{
    total_documents: number;
    per_source_len: number;
    dimension: number;
  }> = {},
) {
  const total = overrides.total_documents ?? 0;
  const perSource = Array.from({ length: overrides.per_source_len ?? 0 }, (_, i) => ({
    source_type: `s${i}`,
    document_count: 1,
  }));
  server.use(
    http.get("*/api/admin/stats", () =>
      HttpResponse.json({
        data: {
          total_documents: total,
          per_source: perSource,
          embedding: {
            provider: "voyage",
            model: "voyage-4-large",
            dimension: overrides.dimension ?? 1024,
          },
        },
      }),
    ),
  );
}

beforeEach(() => {
  setToken("tok");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});
afterEach(() => server.resetHandlers());

// Error-path tests click a confirm button whose onConfirm rejects when the
// mutation errors — the rejection is handled by the mutation's `onError`
// but the outer promise is still unhandled from the DOM event handler.
// Swallow those specific rejections so vitest doesn't flag them.
const suppressUnhandled = (err: unknown) => {
  if (err instanceof Error && /unauthorized|busy/i.test(err.message)) return;
  throw err;
};
process.on("unhandledRejection", suppressUnhandled);

describe("MaintenanceSection", () => {
  it("shows the empty-index hint when total_documents is 0", async () => {
    mockSystemStats({ total_documents: 0 });
    render(<MaintenanceSection />);
    await waitFor(() =>
      expect(
        screen.getByText(/Index is empty today/),
      ).toBeInTheDocument(),
    );
  });

  it("shows the document count and dimension when stats are populated", async () => {
    mockSystemStats({ total_documents: 1234, per_source_len: 2, dimension: 1024 });
    render(<MaintenanceSection />);
    await waitFor(() =>
      expect(screen.getByText(/1,234/)).toBeInTheDocument(),
    );
    expect(screen.getByText(/dimension 1024/)).toBeInTheDocument();
  });

  it("reindex confirm triggers POST /api/reindex and toasts success", async () => {
    mockSystemStats({ total_documents: 1234, per_source_len: 2 });
    let reindexHit = false;
    server.use(
      http.post("*/api/reindex", () => {
        reindexHit = true;
        return HttpResponse.json({
          data: { message: "ok", dimension: 1024, connectors: 3 },
        });
      }),
    );
    render(<MaintenanceSection />);
    await waitFor(() =>
      expect(screen.getByText(/1,234/)).toBeInTheDocument(),
    );
    const runButtons = screen.getAllByRole("button", { name: /^Run$/ });
    await userEvent.click(runButtons[0]);
    const input = await screen.findByPlaceholderText("reindex everything");
    await userEvent.type(input, "reindex everything");
    const cta = await screen.findByRole("button", {
      name: /start re-index/i,
    });
    await waitFor(() => expect(cta).toBeEnabled());
    await userEvent.click(cta);
    await waitFor(() => expect(reindexHit).toBe(true));
    expect(toast.success).toHaveBeenCalledWith(
      expect.stringMatching(/Re-index started on 3 connectors/),
    );
  });

  it("reset-cursors confirm fires DELETE /api/sync/cursors", async () => {
    mockSystemStats();
    let deleted = false;
    server.use(
      http.delete("*/api/sync/cursors", () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    render(<MaintenanceSection />);
    const runButtons = await screen.findAllByRole("button", { name: /^Run$/ });
    await userEvent.click(runButtons[1]);
    const input = await screen.findByPlaceholderText("reset cursors");
    await userEvent.type(input, "reset cursors");
    const cta = await screen.findByRole("button", {
      name: /clear all cursors/i,
    });
    await waitFor(() => expect(cta).toBeEnabled());
    await userEvent.click(cta);
    await waitFor(() => expect(deleted).toBe(true));
    expect(toast.success).toHaveBeenCalledWith("All sync cursors cleared");
  });

  it("reset-cursors surfaces 401 as Unauthorized", async () => {
    mockSystemStats();
    server.use(
      http.delete("*/api/sync/cursors", () =>
        new HttpResponse(null, { status: 401 }),
      ),
    );
    render(<MaintenanceSection />);
    const runButtons = await screen.findAllByRole("button", { name: /^Run$/ });
    await userEvent.click(runButtons[1]);
    const input = await screen.findByPlaceholderText("reset cursors");
    await userEvent.type(input, "reset cursors");
    const cta = await screen.findByRole("button", {
      name: /clear all cursors/i,
    });
    await userEvent.click(cta);
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Unauthorized"));
  });

  it("reindex surfaces API error via toast", async () => {
    mockSystemStats();
    server.use(
      http.post("*/api/reindex", () =>
        HttpResponse.json({ error: "busy" }, { status: 409 }),
      ),
    );
    render(<MaintenanceSection />);
    const runButtons = await screen.findAllByRole("button", { name: /^Run$/ });
    await userEvent.click(runButtons[0]);
    const input = await screen.findByPlaceholderText("reindex everything");
    await userEvent.type(input, "reindex everything");
    const cta = await screen.findByRole("button", {
      name: /start re-index/i,
    });
    await userEvent.click(cta);
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("busy"));
  });
});
