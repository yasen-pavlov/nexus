import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type { RankingSettings } from "@/lib/api-types";
import { searchKeys, settingsKeys } from "@/lib/query-keys";

// The RankingManager hot-swaps these values on save — the next query sees
// them immediately. We invalidate cached search results so the user sees
// the new ordering without a hard refresh.
export function useRankingSettings() {
  const qc = useQueryClient();
  const query = useQuery<RankingSettings>({
    queryKey: settingsKeys.ranking(),
    queryFn: () => fetchAPI<RankingSettings>("/api/settings/ranking"),
    staleTime: 60_000,
  });

  const update = useMutation({
    mutationFn: (next: RankingSettings) =>
      fetchAPI<RankingSettings>("/api/settings/ranking", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      }),
    onSuccess: (data) => {
      qc.setQueryData(settingsKeys.ranking(), data);
      qc.invalidateQueries({ queryKey: searchKeys.all });
      toast.success("Ranking settings saved");
    },
    onError: (err: Error) => toast.error(err.message || "Save failed"),
  });

  return { ...query, update };
}
