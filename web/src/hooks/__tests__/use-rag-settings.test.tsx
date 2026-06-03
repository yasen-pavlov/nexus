import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { type ReactNode } from "react";
import { toast } from "sonner";

import { server } from "@/test/mocks/server";
import { useRAGSettings } from "../use-rag-settings";
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

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

beforeEach(() => {
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  setToken("fake-test-token");
});
afterEach(() => server.resetHandlers());

describe("useRAGSettings", () => {
  it("returns the persisted max_tool_rounds payload", async () => {
    server.use(
      http.get("*/api/settings/rag", () =>
        HttpResponse.json({ data: { max_tool_rounds: 3 } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRAGSettings(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.data?.max_tool_rounds).toBe(3);
  });

  it("update toasts on success and primes the cache", async () => {
    server.use(
      http.get("*/api/settings/rag", () =>
        HttpResponse.json({ data: { max_tool_rounds: 3 } }),
      ),
      http.put("*/api/settings/rag", () =>
        HttpResponse.json({ data: { max_tool_rounds: 1 } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRAGSettings(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      await result.current.update.mutateAsync({
        max_tool_rounds: 1,
        max_images_per_turn: 4,
        enable_multimodal: true,
        enable_open_attachment: false,
      });
    });
    expect(toast.success).toHaveBeenCalledWith("RAG settings saved");
    await waitFor(() =>
      expect(result.current.data?.max_tool_rounds).toBe(1),
    );
  });

  it("surfaces BE validation errors via toast.error", async () => {
    server.use(
      http.get("*/api/settings/rag", () =>
        HttpResponse.json({ data: { max_tool_rounds: 3 } }),
      ),
      http.put("*/api/settings/rag", () =>
        HttpResponse.json(
          { error: "max_tool_rounds must be between 0 and 5" },
          { status: 400 },
        ),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRAGSettings(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      try {
        await result.current.update.mutateAsync({
          max_tool_rounds: 99,
          max_images_per_turn: 4,
          enable_multimodal: true,
          enable_open_attachment: false,
        });
      } catch {
        // expected — non-2xx throws
      }
    });
    expect(toast.error).toHaveBeenCalled();
  });
});
