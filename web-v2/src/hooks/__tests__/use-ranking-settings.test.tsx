import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { type ReactNode } from "react";
import { toast } from "sonner";

import { server } from "@/test/mocks/server";
import { useRankingSettings } from "../use-ranking-settings";
import { setToken } from "@/lib/api-client";
import { searchKeys } from "@/lib/query-keys";

function wrap() {
  // Capture the client outside the Wrapper so the test can introspect the
  // same instance the hook uses (React components re-construct on every
  // render, so a component-local client would be a fresh instance every
  // time and unrelated to whatever the hook sees).
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  return { Wrapper, client };
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

const defaults = {
  source_half_life_days: { telegram: 14, imap: 30, filesystem: 90, paperless: 180 },
  source_recency_floor: { telegram: 0.65, imap: 0.75, filesystem: 0.85, paperless: 0.9 },
  source_trust_weight: { telegram: 0.92, imap: 0.92, filesystem: 1, paperless: 1.05 },
  metadata_bonus_enabled: true,
  source_trust_enabled: true,
  known_source_types: ["imap", "telegram", "paperless", "filesystem"],
};

describe("useRankingSettings", () => {
  it("fetches and returns the active RankingSettings", async () => {
    server.use(
      http.get("*/api/settings/ranking", () =>
        HttpResponse.json({ data: defaults }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRankingSettings(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.data?.source_half_life_days.telegram).toBe(14);
    expect(result.current.data?.metadata_bonus_enabled).toBe(true);
  });

  it("update mutation toasts + invalidates search results on success", async () => {
    server.use(
      http.get("*/api/settings/ranking", () =>
        HttpResponse.json({ data: defaults }),
      ),
      http.put("*/api/settings/ranking", () =>
        HttpResponse.json({
          data: { ...defaults, metadata_bonus_enabled: false },
        }),
      ),
    );
    const { Wrapper, client } = wrap();

    // Spy on invalidateQueries so we can assert the hook calls it with
    // the search key after a successful save. Cached search results go
    // stale on every ranking-knob save because the new weights shift the
    // server-side ordering — spying directly is simpler than building a
    // full observer + gcTime dance to keep a seeded query alive.
    const invalidateSpy = vi.spyOn(client, "invalidateQueries");

    const { result } = renderHook(() => useRankingSettings(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      await result.current.update.mutateAsync({
        ...defaults,
        metadata_bonus_enabled: false,
      });
    });

    expect(toast.success).toHaveBeenCalledWith("Ranking settings saved");
    const invalidatedSearch = invalidateSpy.mock.calls.some(
      (call) => JSON.stringify(call[0]?.queryKey) === JSON.stringify(searchKeys.all),
    );
    expect(invalidatedSearch).toBe(true);
  });

  it("surfaces BE validation errors via toast.error", async () => {
    server.use(
      http.get("*/api/settings/ranking", () =>
        HttpResponse.json({ data: defaults }),
      ),
      http.put("*/api/settings/ranking", () =>
        HttpResponse.json(
          { error: "source_recency_floor[imap] must be in [0,1]" },
          { status: 400 },
        ),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRankingSettings(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      try {
        await result.current.update.mutateAsync({
          ...defaults,
          source_recency_floor: { ...defaults.source_recency_floor, imap: 2 },
        });
      } catch {
        // expected
      }
    });
    expect(toast.error).toHaveBeenCalled();
  });
});
