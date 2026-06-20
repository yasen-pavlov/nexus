import { createContext, useContext } from "react";
import type { SyncJob } from "@/lib/api-types";

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
 * Holds the single sync-jobs controller value. Provided once by
 * {@link import("./sync-jobs-provider").SyncJobsProvider}; null outside it.
 */
export const SyncJobsContext = createContext<UseSyncJobsResult | null>(null);

/**
 * Read the shared sync-jobs state + action surface. Any page can call this;
 * they all see the same live map and one set of toasts. Must be rendered
 * under SyncJobsProvider — keeping a single shared instance is what makes
 * status-transition toasts fire exactly once (a fresh instance would start
 * with an empty prev-status ref and mistake the backend's initial snapshot
 * for newly-observed transitions).
 */
export function useSyncJobs(): UseSyncJobsResult {
  const ctx = useContext(SyncJobsContext);
  if (!ctx) {
    throw new Error("useSyncJobs must be used within a <SyncJobsProvider>");
  }
  return ctx;
}
