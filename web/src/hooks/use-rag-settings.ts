import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type { RAGSettings } from "@/lib/api-types";
import { settingsKeys } from "@/lib/query-keys";

export type UseRAGSettings = ReturnType<typeof useRAGSettings>;

/**
 * Admin RAG runtime knobs — currently the agentic-tool round cap
 * (max_tool_rounds). Hot-swaps server-side; no FE-visible cache to
 * invalidate beyond this query itself.
 */
export function useRAGSettings() {
  const qc = useQueryClient();

  const query = useQuery<RAGSettings>({
    queryKey: settingsKeys.rag(),
    queryFn: () => fetchAPI<RAGSettings>("/api/settings/rag"),
    staleTime: 60_000,
  });

  const update = useMutation({
    mutationFn: (next: RAGSettings) =>
      fetchAPI<RAGSettings>("/api/settings/rag", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      }),
    onSuccess: (data) => {
      qc.setQueryData(settingsKeys.rag(), data);
      toast.success("RAG settings saved");
    },
    onError: (err: Error) => toast.error(err.message || "Save failed"),
  });

  return { ...query, update };
}
