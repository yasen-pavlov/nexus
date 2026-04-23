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

// Capture the live-frame callback passed into openSyncProgressSSE so tests
// can simulate server-sent transitions without booting an EventSource.
let sseOnMessage: ((frame: SyncJob) => void) | null = null;
vi.mock("@/lib/api-client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api-client")>();
  return {
    ...actual,
    openSyncProgressSSE: <T,>(onMessage: (frame: T) => void) => {
      sseOnMessage = onMessage as (frame: SyncJob) => void;
      return { close: () => {} } as unknown as EventSource;
    },
  };
});

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
  sseOnMessage = null;
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

  it("triggerAll surfaces a toast on failure", async () => {
    server.use(
      http.get("*/api/sync", () => HttpResponse.json({ data: [] })),
      http.post("*/api/sync", () =>
        HttpResponse.json({ error: "busy" }, { status: 409 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    let ret: SyncJob[] = [];
    await act(async () => {
      ret = await result.current.triggerAll();
    });
    expect(ret).toEqual([]);
    expect(toast.error).toHaveBeenCalled();
  });

  it("cancelSync surfaces a toast on failure", async () => {
    server.use(
      http.get("*/api/sync", () => HttpResponse.json({ data: [] })),
      http.post("*/api/sync/jobs/job-1/cancel", () =>
        HttpResponse.json({ error: "gone" }, { status: 404 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    await act(async () => {
      await result.current.cancelSync("job-1");
    });
    expect(toast.error).toHaveBeenCalled();
  });

  it("resetCursor surfaces a toast on failure", async () => {
    server.use(
      http.get("*/api/sync", () => HttpResponse.json({ data: [] })),
      http.delete("*/api/sync/cursors/c-1", () =>
        HttpResponse.json({ error: "nope" }, { status: 500 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useSyncJobs(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    await act(async () => {
      await result.current.resetCursor("c-1");
    });
    expect(toast.error).toHaveBeenCalled();
  });
});

describe("useSyncJobs — live SSE transitions", () => {
  function mountWithEmptyList() {
    server.use(http.get("*/api/sync", () => HttpResponse.json({ data: [] })));
    const { Wrapper } = wrap();
    return renderHook(() => useSyncJobs(), { wrapper: Wrapper });
  }

  it("running → completed fires a success toast", async () => {
    const { result } = mountWithEmptyList();
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    act(() => sseOnMessage?.(job({ status: "running" })));
    act(() => sseOnMessage?.(job({ status: "completed" })));
    expect(toast.success).toHaveBeenCalledWith("Sync finished: notes");
  });

  it("running → failed fires an error toast with description", async () => {
    const { result } = mountWithEmptyList();
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    act(() => sseOnMessage?.(job({ status: "running" })));
    act(() => sseOnMessage?.(job({ status: "failed", error: "boom" })));
    expect(toast.error).toHaveBeenCalledWith("Sync failed: notes", {
      description: "boom",
    });
  });

  it("running → canceled fires an info toast", async () => {
    const { result } = mountWithEmptyList();
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    act(() => sseOnMessage?.(job({ status: "running" })));
    act(() => sseOnMessage?.(job({ status: "canceled" })));
    expect(toast.info).toHaveBeenCalledWith("Canceled: notes");
  });

  it("subsequent frames with the same status don't retoast", async () => {
    const { result } = mountWithEmptyList();
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    act(() => sseOnMessage?.(job({ status: "running" })));
    act(() => sseOnMessage?.(job({ status: "completed" })));
    act(() => sseOnMessage?.(job({ status: "completed" })));
    expect(vi.mocked(toast.success).mock.calls.length).toBe(1);
  });

  it("jobsByConnector: running wins over completed for the same connector on reversed arrival", async () => {
    const { result } = mountWithEmptyList();
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    // Completed lands first, running arrives after. The derivation must
    // swap the completed row out for the running one — exercises the
    // "existing.status !== 'running' && job.status === 'running'" branch.
    act(() =>
      sseOnMessage?.(
        job({
          id: "job-done",
          status: "completed",
          started_at: "2026-04-01T00:00:00Z",
        }),
      ),
    );
    act(() =>
      sseOnMessage?.(
        job({
          id: "job-new",
          status: "running",
          started_at: "2026-04-02T00:00:00Z",
        }),
      ),
    );
    expect(result.current.jobsByConnector.get("c-1")?.id).toBe("job-new");
  });

  it("clamps docs_total so the progress bar never regresses mid-run", async () => {
    // Streaming connectors grow their total estimate as they
    // discover work. A later frame with a lower total (because of
    // out-of-order server delivery, a connector that's processed
    // further than it's enumerated, or a late-arriving poll row)
    // would otherwise yank the bar backwards.
    const { result } = mountWithEmptyList();
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    act(() => sseOnMessage?.(job({ docs_total: 500, docs_processed: 10 })));
    act(() => sseOnMessage?.(job({ docs_total: 300, docs_processed: 100 })));
    const clamped = result.current.jobsByConnector.get("c-1");
    expect(clamped?.docs_total).toBe(500);
    expect(clamped?.docs_processed).toBe(100);
  });

  it("lets docs_total rise as the connector discovers more work", async () => {
    const { result } = mountWithEmptyList();
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    act(() => sseOnMessage?.(job({ docs_total: 100, docs_processed: 5 })));
    act(() => sseOnMessage?.(job({ docs_total: 800, docs_processed: 10 })));
    expect(result.current.jobsByConnector.get("c-1")?.docs_total).toBe(800);
  });

  it("jobsByConnector: keeps the existing running row when an older completed arrives later", async () => {
    const { result } = mountWithEmptyList();
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    act(() =>
      sseOnMessage?.(
        job({
          id: "job-running",
          status: "running",
          started_at: "2026-04-02T00:00:00Z",
        }),
      ),
    );
    act(() =>
      sseOnMessage?.(
        job({
          id: "job-old",
          status: "completed",
          started_at: "2026-04-01T00:00:00Z",
        }),
      ),
    );
    expect(result.current.jobsByConnector.get("c-1")?.id).toBe("job-running");
  });
});
