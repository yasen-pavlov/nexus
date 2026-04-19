import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type { BinaryStoreStats, StorageWipeResult } from "@/lib/api-types";
import { adminKeys, storageKeys } from "@/lib/query-keys";
import { formatBytes } from "@/lib/format";

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

  const wipeAll = useMutation({
    mutationFn: async () => {
      const token = localStorage.getItem("nexus_jwt");
      const res = await fetch("/api/storage/cache", {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (res.status === 401) throw new Error("Unauthorized");
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
      return body.data as StorageWipeResult;
    },
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: storageKeys.stats() });
      qc.invalidateQueries({ queryKey: adminKeys.all });
      toast.success(
        `Wiped ${result.deleted_count.toLocaleString()} cached files (${formatBytes(result.bytes_freed)})`,
      );
    },
    onError: (err: Error) => toast.error(err.message || "Wipe failed"),
  });

  const wipeByConnector = useMutation({
    mutationFn: async (connectorId: string) => {
      const token = localStorage.getItem("nexus_jwt");
      const res = await fetch(`/api/storage/cache/${connectorId}`, {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (res.status === 401) throw new Error("Unauthorized");
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
      return body.data as StorageWipeResult;
    },
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: storageKeys.stats() });
      qc.invalidateQueries({ queryKey: adminKeys.all });
      toast.success(
        `Wiped ${result.deleted_count.toLocaleString()} cached files (${formatBytes(result.bytes_freed)})`,
      );
    },
    onError: (err: Error) => toast.error(err.message || "Wipe failed"),
  });

  return { ...query, wipeAll, wipeByConnector };
}
