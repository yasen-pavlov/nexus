import { useQuery } from "@tanstack/react-query";
import { fetchAPI } from "@/lib/api-client";
import type { AdminStats } from "@/lib/api-types";
import { adminKeys } from "@/lib/query-keys";

/**
 * System-wide stats for the admin dashboard. Cached 60s because the numbers
 * don't change faster than sync-job cadence and the page is a read-only
 * overview — a stale count for 30s beats pounding the OpenSearch aggregation
 * on every navigation.
 */
export function useSystemStats() {
  return useQuery<AdminStats>({
    queryKey: adminKeys.stats(),
    queryFn: () => fetchAPI<AdminStats>("/api/admin/stats"),
    staleTime: 60_000,
  });
}
