import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type { LLMSettings } from "@/lib/api-types";
import { llmKeys, settingsKeys } from "@/lib/query-keys";

export type UseLLMSettings = ReturnType<typeof useLLMSettings>;

/**
 * Admin LLM settings — providers (Anthropic / OpenAI / Ollama), default
 * model, and the model allowlist that filters the per-message picker.
 *
 * The mutation invalidates llmKeys.models() because changing a provider key
 * or the allowlist immediately changes which models the picker should show
 * (the BE hot-swaps the registry; the FE just needs to refetch).
 */
export function useLLMSettings() {
  const qc = useQueryClient();

  const query = useQuery<LLMSettings>({
    queryKey: settingsKeys.llm(),
    queryFn: () => fetchAPI<LLMSettings>("/api/settings/llm"),
    staleTime: 60_000,
  });

  const update = useMutation({
    mutationFn: (next: LLMSettings) =>
      fetchAPI<LLMSettings>("/api/settings/llm", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      }),
    onSuccess: (data) => {
      qc.setQueryData(settingsKeys.llm(), data);
      // Bust every llm-prefixed query: post-allowlist models, the
      // pre-allowlist catalog, and the system default. Save can change
      // any of them so a single prefix invalidation is the safest path.
      qc.invalidateQueries({ queryKey: llmKeys.all });
      toast.success("LLM settings saved");
    },
    onError: (err: Error) => toast.error(err.message || "Save failed"),
  });

  return { ...query, update };
}
