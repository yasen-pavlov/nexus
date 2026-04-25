// Pure model-default resolver. Lives apart from chat-thread.tsx so the
// hook can co-export it without tripping react-refresh's "components
// only" check.

import type { Chat, LLMModelInfo } from "@/lib/api-types";

/** Decide which model id the composer should default to. */
export function pickInitialModel(
  chat: Chat | undefined,
  models: LLMModelInfo[],
  lastUsed: string | null,
): string {
  if (chat?.default_model) return chat.default_model;
  if (lastUsed && models.some((m) => m.id === lastUsed)) return lastUsed;
  return models[0]?.id ?? "";
}
