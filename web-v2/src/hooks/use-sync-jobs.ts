import { useEffect, useMemo, useReducer, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI, openSyncProgressSSE } from "@/lib/api-client";
import type { SyncJob } from "@/lib/api-types";
import { connectorKeys, syncKeys } from "@/lib/query-keys";

/**
 * Reducer state. Keyed by job id. The map shape lets live SSE frames
 * update in place without rebuilding the whole list; downstream
 * consumers derive per-connector state via a memoized groupBy.
 */
interface JobsState {
  byId: Map<string, SyncJob>;
}

type Action =
  | { type: "hydrate"; jobs: SyncJob[] }
  | { type: "upsert"; job: SyncJob };

function reducer(state: JobsState, action: Action): JobsState {
  switch (action.type) {
    case "hydrate": {
      const byId = new Map<string, SyncJob>();
      for (const j of action.jobs) byId.set(j.id, j);
      return { byId };
    }
    case "upsert": {
      const byId = new Map(state.byId);
      byId.set(action.job.id, action.job);
      return { byId };
    }
  }
}

const INITIAL: JobsState = { byId: new Map() };

/**
 * Polling floor (ms) for `/api/sync`. The multiplexed SSE covers the
 * fast path; the poll catches missed frames after reconnects + picks up
 * scheduler-initiated jobs the client hasn't seen yet. 15s is the Plan
 * agent's call — short enough that missed events don't linger, long
 * enough not to thrash the cache.
 */
const POLL_INTERVAL_MS = 15_000;

export interface UseSyncJobsResult {
  /** Every running/recent job keyed by connector id (newest run wins). */
  jobsByConnector: Map<string, SyncJob>;
  /** Raw id-keyed map in case a caller needs job-id lookups. */
  jobsById: Map<string, SyncJob>;
  isLoading: boolean;
  triggerSync: (connectorId: string) => Promise<SyncJob | null>;
  cancelSync: (jobId: string) => Promise<void>;
  triggerAll: () => Promise<SyncJob[]>;
  resetCursor: (connectorId: string) => Promise<void>;
}

/**
 * Global sync-jobs state + action surface.
 *
 * Mounted once in AppShell so the SSE subscription survives route changes.
 * Any page can `useSyncJobs()` and read the same live map; status
 * transitions fire toasts exactly once thanks to a prev-status ref.
 */
export function useSyncJobs(): UseSyncJobsResult {
  const [state, dispatch] = useReducer(reducer, INITIAL);
  const queryClient = useQueryClient();

  // Periodic poll — safety net for SSE reconnects + scheduler wake-ups.
  const list = useQuery<SyncJob[]>({
    queryKey: syncKeys.jobs(),
    queryFn: () => fetchAPI<SyncJob[]>("/api/sync"),
    refetchInterval: POLL_INTERVAL_MS,
    refetchOnWindowFocus: true,
    staleTime: POLL_INTERVAL_MS,
  });

  // Hydrate the reducer from the polled list — running jobs populate the
  // map on mount even if the SSE stream hasn't delivered a frame yet.
  useEffect(() => {
    if (list.data) dispatch({ type: "hydrate", jobs: list.data });
  }, [list.data]);

  // Previous-status ref so we only toast on transitions, not on every
  // progress frame. Keyed by job id; entries cleared when the job leaves
  // the map (we don't need to retain status for jobs that are gone).
  const prevStatusRef = useRef<Map<string, SyncJob["status"]>>(new Map());

  // Open the multiplexed SSE exactly once per mount.
  useEffect(() => {
    const es = openSyncProgressSSE<SyncJob>((job) => {
      dispatch({ type: "upsert", job });

      const prev = prevStatusRef.current.get(job.id);
      const name = job.connector_name || "connector";
      if (prev !== job.status) {
        if (prev === "running" || prev === undefined) {
          // running → terminal: announce + invalidate caches so the
          // connector list + activity timeline refetch cleanly.
          if (job.status === "completed") {
            toast.success(`Sync finished: ${name}`);
            queryClient.invalidateQueries({ queryKey: connectorKeys.all });
          } else if (job.status === "failed") {
            toast.error(`Sync failed: ${name}`, {
              description: job.error || undefined,
            });
            queryClient.invalidateQueries({ queryKey: connectorKeys.all });
          } else if (job.status === "canceled") {
            toast.info(`Canceled: ${name}`);
            queryClient.invalidateQueries({ queryKey: connectorKeys.all });
          }
        }
      }
      prevStatusRef.current.set(job.id, job.status);
    });
    return () => {
      es?.close();
    };
    // Empty deps — the hook is meant to be mounted once in AppShell.
    // Nothing inside the effect references props or state that changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Derive: newest job per connector. When both a running and a recent
  // completed job exist for the same connector, prefer the running one
  // (higher informational density); otherwise take the latest started_at.
  const jobsByConnector = useMemo(() => {
    const map = new Map<string, SyncJob>();
    for (const job of state.byId.values()) {
      const existing = map.get(job.connector_id);
      if (!existing) {
        map.set(job.connector_id, job);
        continue;
      }
      if (existing.status === "running" && job.status !== "running") continue;
      if (existing.status !== "running" && job.status === "running") {
        map.set(job.connector_id, job);
        continue;
      }
      if (new Date(job.started_at) > new Date(existing.started_at)) {
        map.set(job.connector_id, job);
      }
    }
    return map;
  }, [state.byId]);

  async function triggerSync(connectorId: string): Promise<SyncJob | null> {
    try {
      const job = await fetchAPI<SyncJob>(`/api/sync/${connectorId}`, {
        method: "POST",
      });
      if (job) dispatch({ type: "upsert", job });
      return job;
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to start sync");
      return null;
    }
  }

  async function cancelSync(jobId: string): Promise<void> {
    try {
      await fetchAPI(`/api/sync/jobs/${jobId}/cancel`, { method: "POST" });
      // Terminal frame will arrive via SSE; no optimistic UI needed.
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to cancel");
    }
  }

  async function triggerAll(): Promise<SyncJob[]> {
    try {
      const jobs = await fetchAPI<SyncJob[]>("/api/sync", { method: "POST" });
      for (const j of jobs ?? []) dispatch({ type: "upsert", job: j });
      return jobs ?? [];
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to start syncs");
      return [];
    }
  }

  async function resetCursor(connectorId: string): Promise<void> {
    try {
      await fetchAPI(`/api/sync/cursors/${connectorId}`, { method: "DELETE" });
      toast.success("Sync cursor cleared — the next sync will be a full re-index.");
      queryClient.invalidateQueries({ queryKey: connectorKeys.all });
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to reset cursor");
    }
  }

  return {
    jobsByConnector,
    jobsById: state.byId,
    isLoading: list.isPending,
    triggerSync,
    cancelSync,
    triggerAll,
    resetCursor,
  };
}
