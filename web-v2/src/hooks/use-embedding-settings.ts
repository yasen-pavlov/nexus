import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type { EmbeddingSettings, RerankSettings } from "@/lib/api-types";
import { searchKeys, settingsKeys } from "@/lib/query-keys";

export function useEmbeddingSettings() {
  const qc = useQueryClient();

  const query = useQuery<EmbeddingSettings>({
    queryKey: settingsKeys.embedding(),
    queryFn: () => fetchAPI<EmbeddingSettings>("/api/settings/embedding"),
    staleTime: 60_000,
  });

  const update = useMutation({
    mutationFn: (next: EmbeddingSettings) =>
      fetchAPI<EmbeddingSettings>("/api/settings/embedding", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      }),
    onSuccess: (data) => {
      qc.setQueryData(settingsKeys.embedding(), data);
      // Changing embedding provider/model kicks off a full reindex on the
      // server — invalidate anything that reflects index + sync state so
      // the dashboard refreshes while the server works.
      qc.invalidateQueries({ queryKey: ["admin"] });
      qc.invalidateQueries({ queryKey: ["connectors"] });
      qc.invalidateQueries({ queryKey: ["sync"] });
      toast.success("Embedding settings saved");
    },
    onError: (err: Error) => toast.error(err.message || "Save failed"),
  });

  return { ...query, update };
}

export function useRerankSettings() {
  const qc = useQueryClient();

  const query = useQuery<RerankSettings>({
    queryKey: settingsKeys.rerank(),
    queryFn: () => fetchAPI<RerankSettings>("/api/settings/rerank"),
    staleTime: 60_000,
  });

  const update = useMutation({
    mutationFn: (next: RerankSettings) =>
      fetchAPI<RerankSettings>("/api/settings/rerank", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      }),
    onSuccess: (data) => {
      qc.setQueryData(settingsKeys.rerank(), data);
      qc.invalidateQueries({ queryKey: ["admin"] });
      // The min-score floor is part of the ranking config — hot-swap
      // happens server-side, but the cached ranking query data + any
      // cached search results are now stale.
      qc.invalidateQueries({ queryKey: settingsKeys.ranking() });
      qc.invalidateQueries({ queryKey: searchKeys.all });
      toast.success("Reranking settings saved");
    },
    onError: (err: Error) => toast.error(err.message || "Save failed"),
  });

  return { ...query, update };
}
