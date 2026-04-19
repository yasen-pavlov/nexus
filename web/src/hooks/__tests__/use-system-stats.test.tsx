import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { type ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { useSystemStats } from "../use-system-stats";
import { setToken } from "@/lib/api-client";

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

beforeEach(() => setToken("fake-test-token"));
afterEach(() => server.resetHandlers());

describe("useSystemStats", () => {
  it("fetches and returns AdminStats payload", async () => {
    server.use(
      http.get("*/api/admin/stats", () =>
        HttpResponse.json({
          data: {
            total_documents: 10,
            total_chunks: 20,
            users_count: 2,
            per_source: [
              {
                source_type: "imap",
                source_name: "inbox",
                document_count: 10,
                chunk_count: 20,
                latest_indexed_at: "2026-04-18T10:00:00Z",
                cache_count: 1,
                cache_bytes: 2048,
              },
            ],
            embedding: {
              enabled: true,
              provider: "voyage",
              model: "voyage-3-large",
              dimension: 1024,
            },
            rerank: { enabled: false, provider: "", model: "" },
          },
        }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSystemStats(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.data?.total_documents).toBe(10);
    expect(result.current.data?.per_source).toHaveLength(1);
    expect(result.current.data?.embedding.dimension).toBe(1024);
  });

  it("surfaces fetch errors via TanStack Query's error", async () => {
    server.use(
      http.get("*/api/admin/stats", () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSystemStats(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.error).toBeTruthy());
    expect(result.current.error?.message).toBe("boom");
  });
});
