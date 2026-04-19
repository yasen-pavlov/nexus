import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { type ReactNode } from "react";
import { toast } from "sonner";

import { server } from "@/test/mocks/server";
import { useSyncJobs } from "../use-sync-jobs";
import type { SyncJob } from "@/lib/api-types";
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
  return { Wrapper, client };
}

function job(overrides: Partial<SyncJob> = {}): SyncJob {
  return {
    id: "job-1",
    connector_id: "c-1",
    connector_name: "notes",
    connector_type: "filesystem",
    status: "running",
    docs_total: 100,
    docs_processed: 0,
    docs_deleted: 0,
    errors: 0,
    error: "",
    started_at: "2026-04-18T00:00:00Z",
    completed_at: "",
    ...overrides,
  };
}

// Spy on sonner so we can assert toast transitions. Each test resets
// the spies in beforeEach.
vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

beforeEach(() => {
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  vi.mocked(toast.info).mockClear();
  setToken("fake-test-token");
});
afterEach(() => {
  server.resetHandlers();
});

describe("useSyncJobs — list hydration", () => {
  it("populates jobsByConnector from the polled /api/sync response", async () => {
    server.use(
      http.get("*/api/sync", () =>
        HttpResponse.json({ data: [job({ status: "running" })] }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });

    await waitFor(() => {
      expect(result.current.jobsByConnector.size).toBe(1);
    });
    const running = result.current.jobsByConnector.get("c-1");
    expect(running?.status).toBe("running");
  });

  it("prefers the running row over a stale completed row for the same connector", async () => {
    const running = job({ id: "job-new", status: "running" });
    const completed = job({
      id: "job-old",
      status: "completed",
      started_at: "2026-04-17T00:00:00Z",
      completed_at: "2026-04-17T01:00:00Z",
    });
    server.use(
      http.get("*/api/sync", () =>
        HttpResponse.json({ data: [completed, running] }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => {
      expect(result.current.jobsByConnector.size).toBe(1);
    });
    expect(result.current.jobsByConnector.get("c-1")?.id).toBe("job-new");
  });

  it("picks the newest completed row when no running row is present", async () => {
    const older = job({
      id: "job-old",
      status: "completed",
      started_at: "2026-04-17T00:00:00Z",
    });
    const newer = job({
      id: "job-newer",
      status: "completed",
      started_at: "2026-04-18T00:00:00Z",
    });
    server.use(
      http.get("*/api/sync", () =>
        HttpResponse.json({ data: [older, newer] }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => {
      expect(result.current.jobsByConnector.size).toBe(1);
    });
    expect(result.current.jobsByConnector.get("c-1")?.id).toBe("job-newer");
  });
});

describe("useSyncJobs — mutations", () => {
  it("triggerSync posts to /api/sync/:id and returns the new job", async () => {
    server.use(
      http.get("*/api/sync", () => HttpResponse.json({ data: [] })),
      http.post("*/api/sync/c-1", () =>
        HttpResponse.json({ data: job({ id: "job-fresh" }) }, { status: 202 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    const started: { value: SyncJob | null } = { value: null };
    await act(async () => {
      started.value = await result.current.triggerSync("c-1");
    });
    expect(started.value?.id).toBe("job-fresh");
  });

  it("triggerSync surfaces a toast on API failure", async () => {
    server.use(
      http.get("*/api/sync", () => HttpResponse.json({ data: [] })),
      http.post("*/api/sync/c-1", () =>
        HttpResponse.json({ error: "already running" }, { status: 409 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await result.current.triggerSync("c-1");
    });
    expect(vi.mocked(toast.error)).toHaveBeenCalledOnce();
  });

  it("cancelSync POSTs to the jobs/:id/cancel endpoint", async () => {
    let cancelHit = false;
    server.use(
      http.get("*/api/sync", () => HttpResponse.json({ data: [] })),
      http.post("*/api/sync/jobs/job-1/cancel", () => {
        cancelHit = true;
        return HttpResponse.json({ data: { message: "ok" } }, { status: 202 });
      }),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    await act(async () => {
      await result.current.cancelSync("job-1");
    });
    expect(cancelHit).toBe(true);
  });

  it("resetCursor DELETEs the per-connector cursor + fires a success toast", async () => {
    server.use(
      http.get("*/api/sync", () => HttpResponse.json({ data: [] })),
      http.delete("*/api/sync/cursors/c-1", () =>
        HttpResponse.json({ data: { message: "cleared" } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    await act(async () => {
      await result.current.resetCursor("c-1");
    });
    expect(vi.mocked(toast.success)).toHaveBeenCalledOnce();
  });

  it("triggerAll POSTs to /api/sync and returns the job list", async () => {
    server.use(
      http.get("*/api/sync", () => HttpResponse.json({ data: [] })),
      http.post("*/api/sync", () =>
        HttpResponse.json(
          { data: [job({ id: "j-a", connector_id: "c-a" }), job({ id: "j-b", connector_id: "c-b" })] },
          { status: 202 },
        ),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    let returned: SyncJob[] = [];
    await act(async () => {
      returned = await result.current.triggerAll();
    });
    expect(returned).toHaveLength(2);
  });
});
