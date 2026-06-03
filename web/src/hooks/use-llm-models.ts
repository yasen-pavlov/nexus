import { useQuery } from "@tanstack/react-query";

import { fetchAPI } from "@/lib/api-client";
import type { LLMModelInfo } from "@/lib/api-types";
import { llmKeys } from "@/lib/query-keys";

/**
 * Visible LLM models per the configured providers + admin allowlist.
 * Used by the per-message picker in the Ask composer.
 */
export function useLLMModels() {
  return useQuery<LLMModelInfo[]>({
    queryKey: llmKeys.models(),
    queryFn: () => fetchAPI<LLMModelInfo[]>("/api/llm/models"),
    staleTime: 60_000,
  });
}

/**
 * Pre-allowlist catalog: every model whose provider has a key, regardless
 * of whether the admin has unticked it. Admin-only — the BE returns 403
 * for non-admin callers. Used by the allowlist editor so deselecting a
 * row doesn't make the row vanish from the editor itself.
 */
export function useLLMCatalog() {
  return useQuery<LLMModelInfo[]>({
    queryKey: llmKeys.catalog(),
    queryFn: () =>
      fetchAPI<LLMModelInfo[]>("/api/llm/models?include_disallowed=true"),
    staleTime: 60_000,
  });
}

/**
 * System-wide default model id (provider-prefixed) per the admin
 * settings. Used as the per-message picker fallback so admin choices
 * propagate to existing browsers without a localStorage clear.
 */
export function useLLMDefault() {
  return useQuery<{ default_model: string }>({
    queryKey: llmKeys.defaultModel(),
    queryFn: () => fetchAPI<{ default_model: string }>("/api/llm/default"),
    staleTime: 60_000,
  });
}
