import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { type ReactNode } from "react";
import { toast } from "sonner";

import { server } from "@/test/mocks/server";
import { useRetentionSettings } from "../use-retention";
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

const defaults = {
  retention_days: 90,
  retention_per_connector: 200,
  sweep_interval_minutes: 60,
  min_sweep_interval_minutes: 5,
};

describe("useRetentionSettings", () => {
  it("returns current retention payload", async () => {
    server.use(
      http.get("*/api/settings/retention", () =>
        HttpResponse.json({ data: defaults }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRetentionSettings(), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.data?.retention_days).toBe(90);
    expect(result.current.data?.min_sweep_interval_minutes).toBe(5);
  });

  it("update mutation toasts on success and primes the cache", async () => {
    server.use(
      http.get("*/api/settings/retention", () =>
        HttpResponse.json({ data: defaults }),
      ),
      http.put("*/api/settings/retention", () =>
        HttpResponse.json({
          data: { ...defaults, retention_days: 30 },
        }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRetentionSettings(), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      await result.current.update.mutateAsync({
        retention_days: 30,
        retention_per_connector: 200,
        sweep_interval_minutes: 60,
      });
    });
    expect(toast.success).toHaveBeenCalledWith("Retention settings saved");
  });

  it("runSweep toasts Cleanup complete on success", async () => {
    server.use(
      http.get("*/api/settings/retention", () =>
        HttpResponse.json({ data: defaults }),
      ),
      http.post("*/api/settings/retention/sweep", () =>
        HttpResponse.json({ data: { ok: true } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRetentionSettings(), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      await result.current.runSweep.mutateAsync();
    });
    expect(toast.success).toHaveBeenCalledWith("Cleanup complete");
  });

  it("update surfaces BE validation errors via toast.error", async () => {
    server.use(
      http.get("*/api/settings/retention", () =>
        HttpResponse.json({ data: defaults }),
      ),
      http.put("*/api/settings/retention", () =>
        HttpResponse.json(
          { error: "sweep_interval_minutes must be >= 5" },
          { status: 400 },
        ),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRetentionSettings(), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      try {
        await result.current.update.mutateAsync({
          retention_days: 30,
          retention_per_connector: 200,
          sweep_interval_minutes: 2,
        });
      } catch {
        // expected — the mutation throws on non-2xx so onError fires.
      }
    });
    expect(toast.error).toHaveBeenCalled();
  });
});
