import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type { BinaryStoreStats, StorageWipeResult } from "@/lib/api-types";
import { adminKeys, storageKeys } from "@/lib/query-keys";
import { formatBytes } from "@/lib/format";

/**
 * Issues a cache-wipe DELETE and unwraps the `{ data }` envelope. Both wipe
 * mutations (whole cache vs. single connector) differ only in their URL, so the
 * hand-rolled fetch + auth header + error mapping lives here once. A 401 maps to
 * "Unauthorized"; any other non-2xx surfaces the server's error body.
 */
async function wipeCache(url: string): Promise<StorageWipeResult> {
  const token = localStorage.getItem("nexus_jwt");
  const res = await fetch(url, {
    method: "DELETE",
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (res.status === 401) throw new Error("Unauthorized");
  const body = await res.json();
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body.data as StorageWipeResult;
}

/**
 * Binary-cache stats per (source_type, source_name), plus mutations to wipe
 * a single connector's cache or the entire cache. Cache wipes cascade to
 * the admin stats query because cache_count/cache_bytes live on that
 * response too.
 */
export function useStorageStats() {
  const qc = useQueryClient();

  const query = useQuery<BinaryStoreStats[]>({
    queryKey: storageKeys.stats(),
    queryFn: () => fetchAPI<BinaryStoreStats[]>("/api/storage/stats"),
    staleTime: 30_000,
  });

  // Both wipes share success (invalidate stats + admin, toast the counts) and
  // error (toast the message) handling — only the request URL differs.
  const onWipeSuccess = (result: StorageWipeResult) => {
    qc.invalidateQueries({ queryKey: storageKeys.stats() });
    qc.invalidateQueries({ queryKey: adminKeys.all });
    toast.success(
      `Wiped ${result.deleted_count.toLocaleString()} cached files (${formatBytes(result.bytes_freed)})`,
    );
  };
  const onWipeError = (err: Error) => toast.error(err.message || "Wipe failed");

  const wipeAll = useMutation({
    mutationFn: () => wipeCache("/api/storage/cache"),
    onSuccess: onWipeSuccess,
    onError: onWipeError,
  });

  const wipeByConnector = useMutation({
    mutationFn: (connectorId: string) =>
      wipeCache(`/api/storage/cache/${connectorId}`),
    onSuccess: onWipeSuccess,
    onError: onWipeError,
  });

  return { ...query, wipeAll, wipeByConnector };
}
