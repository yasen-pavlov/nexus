import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";

import { render, screen, userEvent, waitFor } from "@/test/test-utils";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { CacheSection } from "../cache-section";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

const statsRows = [
  { source_type: "telegram", source_name: "chats", count: 3, total_size: 2048 },
  { source_type: "filesystem", source_name: "docs", count: 1, total_size: 512 },
];

const connectors = [
  { id: "c-tg", type: "telegram", name: "chats", shared: false, enabled: true, schedule: "", config: {} },
  { id: "c-fs", type: "filesystem", name: "docs", shared: true, enabled: true, schedule: "", config: {} },
];

function mockStats(rows = statsRows) {
  server.use(
    http.get("*/api/storage/stats", () => HttpResponse.json({ data: rows })),
    http.get("*/api/connectors/", () => HttpResponse.json({ data: connectors })),
  );
}

beforeEach(() => {
  setToken("tok");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});
afterEach(() => server.resetHandlers());

describe("CacheSection", () => {
  it("renders a row per source with totals", async () => {
    mockStats();
    render(<CacheSection />);
    await waitFor(() =>
      expect(screen.getByText(/Wipe everything/)).toBeInTheDocument(),
    );
    expect(screen.getByText("chats")).toBeInTheDocument();
    expect(screen.getByText("docs")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^Wipe$/ })).toHaveLength(2);
  });

  it("shows empty state when stats list is empty", async () => {
    mockStats([]);
    render(<CacheSection />);
    await waitFor(() =>
      expect(screen.getByText(/No cached binaries yet/)).toBeInTheDocument(),
    );
  });

  it("per-row wipe opens the typed dialog and fires DELETE on confirm", async () => {
    mockStats();
    let wipedID = "";
    server.use(
      http.delete("*/api/storage/cache/:id", ({ params }) => {
        wipedID = params.id as string;
        return HttpResponse.json({
          data: { deleted_count: 3, bytes_freed: 2048 },
        });
      }),
    );

    render(<CacheSection />);
    await waitFor(() => expect(screen.getByText("chats")).toBeInTheDocument());

    await userEvent.click(screen.getAllByRole("button", { name: /^Wipe$/ })[0]);
    // Dialog surfaces the Telegram warning because the row is telegram.
    await waitFor(() =>
      expect(screen.getByText(/Telegram eager-caches/)).toBeInTheDocument(),
    );

    const input = screen.getByPlaceholderText("chats");
    await userEvent.type(input, "chats");
    const cta = await screen.findByRole("button", { name: /wipe cache/i });
    await waitFor(() => expect(cta).toBeEnabled());
    await userEvent.click(cta);
    await waitFor(() => expect(wipedID).toBe("c-tg"));
    expect(toast.success).toHaveBeenCalled();
  });

  it("wipe-all flow fires DELETE /api/storage/cache", async () => {
    mockStats();
    let hit = false;
    server.use(
      http.delete("*/api/storage/cache", () => {
        hit = true;
        return HttpResponse.json({
          data: { deleted_count: 4, bytes_freed: 2560 },
        });
      }),
    );

    render(<CacheSection />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /Wipe all/ })).toBeInTheDocument(),
    );
    await userEvent.click(screen.getByRole("button", { name: /Wipe all/ }));
    const input = await screen.findByPlaceholderText("wipe everything");
    await userEvent.type(input, "wipe everything");
    const cta = await screen.findByRole("button", { name: /wipe all caches/i });
    await waitFor(() => expect(cta).toBeEnabled());
    await userEvent.click(cta);
    await waitFor(() => expect(hit).toBe(true));
  });
});
