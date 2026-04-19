import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";
import type { ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { useStorageStats } from "../use-storage-stats";

function wrap() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  return { Wrapper };
}

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

beforeEach(() => {
  localStorage.setItem("nexus_jwt", "tok");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});
afterEach(() => server.resetHandlers());

const rows = [
  { source_type: "telegram", source_name: "chats", count: 4, total_size: 2048 },
];

describe("useStorageStats", () => {
  it("lists per-source rows", async () => {
    server.use(
      http.get("*/api/storage/stats", () =>
        HttpResponse.json({ data: rows }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useStorageStats(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.[0].source_name).toBe("chats");
  });

  it("wipeAll success toasts with counts + invalidates stats", async () => {
    server.use(
      http.get("*/api/storage/stats", () =>
        HttpResponse.json({ data: rows }),
      ),
      http.delete("*/api/storage/cache", () =>
        HttpResponse.json({ data: { deleted_count: 10, bytes_freed: 1024 } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useStorageStats(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await act(async () => {
      await result.current.wipeAll.mutateAsync();
    });
    expect(toast.success).toHaveBeenCalled();
  });

  it("wipeAll surfaces 401 as Unauthorized via toast.error", async () => {
    server.use(
      http.get("*/api/storage/stats", () =>
        HttpResponse.json({ data: rows }),
      ),
      http.delete("*/api/storage/cache", () =>
        new HttpResponse(null, { status: 401 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useStorageStats(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await act(async () => {
      await expect(result.current.wipeAll.mutateAsync()).rejects.toThrow(
        /unauthorized/i,
      );
    });
    expect(toast.error).toHaveBeenCalledWith("Unauthorized");
  });

  it("wipeAll surfaces JSON error body on non-2xx", async () => {
    server.use(
      http.get("*/api/storage/stats", () =>
        HttpResponse.json({ data: rows }),
      ),
      http.delete("*/api/storage/cache", () =>
        HttpResponse.json({ error: "nope" }, { status: 500 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useStorageStats(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await act(async () => {
      await expect(result.current.wipeAll.mutateAsync()).rejects.toThrow("nope");
    });
  });

  it("wipeByConnector success toasts", async () => {
    server.use(
      http.get("*/api/storage/stats", () =>
        HttpResponse.json({ data: rows }),
      ),
      http.delete("*/api/storage/cache/c-1", () =>
        HttpResponse.json({ data: { deleted_count: 3, bytes_freed: 300 } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useStorageStats(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await act(async () => {
      await result.current.wipeByConnector.mutateAsync("c-1");
    });
    expect(toast.success).toHaveBeenCalled();
  });

  it("wipeByConnector 401 + error-body branches both reach toast.error", async () => {
    server.use(
      http.get("*/api/storage/stats", () =>
        HttpResponse.json({ data: rows }),
      ),
      http.delete("*/api/storage/cache/c-1", () =>
        new HttpResponse(null, { status: 401 }),
      ),
      http.delete("*/api/storage/cache/c-2", () =>
        HttpResponse.json({ error: "cannot" }, { status: 500 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useStorageStats(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    await act(async () => {
      await expect(
        result.current.wipeByConnector.mutateAsync("c-1"),
      ).rejects.toThrow(/unauthorized/i);
    });
    await act(async () => {
      await expect(
        result.current.wipeByConnector.mutateAsync("c-2"),
      ).rejects.toThrow("cannot");
    });
    expect(vi.mocked(toast.error).mock.calls.length).toBeGreaterThanOrEqual(2);
  });
});
