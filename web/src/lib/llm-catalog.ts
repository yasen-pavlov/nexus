// LLM (answer-generation) catalog the admin form renders. Mirrors
// internal/llm/catalog.go. Verified 2026-11-13; admins can also add custom
// IDs from the allowlist editor when a freshly-released model isn't here yet.

import type { ModelOption } from "@/lib/model-catalog";

export type LLMProvider = "anthropic" | "openai" | "ollama";

export const LLM_PROVIDERS: { value: LLMProvider; label: string; hint?: string }[] = [
  { value: "anthropic", label: "Anthropic", hint: "Claude — native citations" },
  { value: "openai", label: "OpenAI", hint: "GPT-6 family" },
  { value: "ollama", label: "Ollama", hint: "Self-hosted local models" },
];

// Curated bare-id options surfaced by the model combobox per provider. The
// values stored on the BE are provider-prefixed (e.g. "anthropic:claude-…"),
// but the admin combobox edits the bare id and the form prefixes it on save.
export const LLM_BARE_MODELS: Record<LLMProvider, ModelOption[]> = {
  anthropic: [
    { value: "claude-fable-5-2", label: "claude-fable-5-2", notes: "Deepest reasoning" },
    { value: "claude-opus-5-5", label: "claude-opus-5-5", notes: "Flagship synthesis" },
    { value: "claude-sonnet-5-5", label: "claude-sonnet-5-5", notes: "Recommended default" },
    { value: "claude-haiku-5-5", label: "claude-haiku-5-5", notes: "Fast / cheap rewriter" },
  ],
  openai: [
    { value: "gpt-6-astra", label: "gpt-6-astra", notes: "Flagship" },
    { value: "gpt-6.1-sol", label: "gpt-6.1-sol", notes: "Recommended default" },
    { value: "gpt-6.1-terra", label: "gpt-6.1-terra", notes: "Fast" },
    { value: "gpt-6-luna", label: "gpt-6-luna", notes: "Fast / cheap rewriter" },
  ],
  ollama: [
    { value: "qwen3:14b", label: "qwen3:14b", notes: "16GB VRAM" },
    { value: "gemma3:12b", label: "gemma3:12b", notes: "16GB VRAM" },
    { value: "command-r:35b", label: "command-r:35b", notes: "24GB; built-in citations" },
    { value: "llama3.2-vision:11b", label: "llama3.2-vision:11b", notes: "Local vision" },
  ],
};

// Default bare model when admin picks a provider in the combobox. Matches the
// "Recommended default" entry above per provider, and the backend's fallback
// (recommendedDefaults in internal/llm/catalog.go).
export const DEFAULT_LLM_BARE_MODEL: Record<LLMProvider, string> = {
  anthropic: "claude-sonnet-5-5",
  openai: "gpt-6.1-sol",
  ollama: "qwen3:14b",
};

// Default bare model for the rewriter. It runs before every follow-up
// question inside a 3s budget, so it is the provider's fastest model, not
// its recommended answer model.
export const DEFAULT_LLM_REWRITER_BARE_MODEL: Record<LLMProvider, string> = {
  anthropic: "claude-haiku-5-5",
  openai: "gpt-6-luna",
  ollama: "qwen3:14b",
};

// Helpers for splitting provider:bareID and rejoining without polluting the
// admin form with string-manipulation noise.
export function splitModelID(id: string): { provider: LLMProvider | ""; bare: string } {
  // Defensive: a fresh install (or a backend returning null) can leave the
  // default model unset. Without this guard, id.indexOf would throw and take
  // the whole admin Settings page down via the error boundary.
  if (typeof id !== "string" || id === "") return { provider: "", bare: "" };
  const colon = id.indexOf(":");
  if (colon <= 0 || colon === id.length - 1) return { provider: "", bare: "" };
  const provider = id.slice(0, colon);
  if (provider !== "anthropic" && provider !== "openai" && provider !== "ollama") {
    return { provider: "", bare: id };
  }
  return { provider, bare: id.slice(colon + 1) };
}

export function joinModelID(provider: LLMProvider, bare: string): string {
  return `${provider}:${bare}`;
}
