import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { fetchAPI } from "@/lib/api-client";
import type { ConnectorConfig } from "@/lib/api-types";
import { connectorKeys } from "@/lib/query-keys";

export interface SyncStripStats {
  sourceCount: number;
  lastSyncAt?: string;
}

/**
 * Derived stats for the top-bar telegraph. Client-derived from the
 * existing connectors list — no new backend endpoint. Doc count is
 * deliberately absent for Phase 3: there's no cheap aggregate for it
 * today, and "N sources · last synced Xm ago" is informative enough.
 * Adding a /api/stats aggregate endpoint is backlog.
 */
export function useSyncStats(): SyncStripStats {
  const { data } = useQuery<ConnectorConfig[]>({
    queryKey: connectorKeys.list(),
    queryFn: () => fetchAPI<ConnectorConfig[]>("/api/connectors/"),
    staleTime: 30_000,
  });

  return useMemo<SyncStripStats>(() => {
    const connectors = data ?? [];
    // Unique source_type count — each connector type is a "source"; the
    // user may run multiple connectors of the same type, but the
    // top-bar only cares about variety, not multiplicity.
    const typeSet = new Set<string>();
    let latest: string | undefined;
    for (const c of connectors) {
      typeSet.add(c.type);
      if (c.last_run && (!latest || c.last_run > latest)) {
        latest = c.last_run;
      }
    }
    return { sourceCount: typeSet.size, lastSyncAt: latest };
  }, [data]);
}
