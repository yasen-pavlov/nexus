// Pure model-default resolver. Lives apart from chat-thread.tsx so the
// hook can co-export it without tripping react-refresh's "components
// only" check.

import type { Chat, LLMModelInfo } from "@/lib/api-types";

/**
 * Decide which model id the composer should default to. Priority:
 *   1. chat.default_model — explicit per-chat override.
 *   2. systemDefault       — admin's `/api/settings/llm` default. Beats
 *                            the user's lastUsed so admin changes
 *                            propagate to existing browsers without a
 *                            localStorage clear.
 *   3. lastUsed            — sticky personal preference for this
 *                            browser, when still in the visible set.
 *   4. models[0]           — fallback to the first visible model.
 */
export function pickInitialModel(
  chat: Chat | undefined,
  models: LLMModelInfo[],
  lastUsed: string | null,
  systemDefault?: string | null,
): string {
  if (chat?.default_model) return chat.default_model;
  if (systemDefault && models.some((m) => m.id === systemDefault)) {
    return systemDefault;
  }
  if (lastUsed && models.some((m) => m.id === lastUsed)) return lastUsed;
  return models[0]?.id ?? "";
}
