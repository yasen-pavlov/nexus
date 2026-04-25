import { useQuery } from "@tanstack/react-query";

import { fetchAPI } from "@/lib/api-client";
import type { LLMModelInfo } from "@/lib/api-types";
import { llmKeys } from "@/lib/query-keys";

/**
 * Visible LLM models per the configured providers + admin allowlist. Used by
 * both the admin allowlist editor (to render the catalog) and the per-message
 * picker in the Ask composer (Phase 3).
 */
export function useLLMModels() {
  return useQuery<LLMModelInfo[]>({
    queryKey: llmKeys.models(),
    queryFn: () => fetchAPI<LLMModelInfo[]>("/api/llm/models"),
    staleTime: 60_000,
  });
}
