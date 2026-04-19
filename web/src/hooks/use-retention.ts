import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type { RetentionSettings, RetentionSettingsUpdate } from "@/lib/api-types";
import { settingsKeys } from "@/lib/query-keys";

export function useRetentionSettings() {
  const qc = useQueryClient();
  const query = useQuery<RetentionSettings>({
    queryKey: settingsKeys.retention(),
    queryFn: () => fetchAPI<RetentionSettings>("/api/settings/retention"),
    staleTime: 60_000,
  });

  const update = useMutation({
    mutationFn: (next: RetentionSettingsUpdate) =>
      fetchAPI<RetentionSettings>("/api/settings/retention", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      }),
    onSuccess: (data) => {
      qc.setQueryData(settingsKeys.retention(), data);
      toast.success("Retention settings saved");
    },
    onError: (err: Error) => toast.error(err.message || "Save failed"),
  });

  const runSweep = useMutation({
    mutationFn: () =>
      fetchAPI<{ ok: boolean }>("/api/settings/retention/sweep", {
        method: "POST",
      }),
    onSuccess: () => toast.success("Cleanup complete"),
    onError: (err: Error) => toast.error(err.message || "Sweep failed"),
  });

  return { ...query, update, runSweep };
}
