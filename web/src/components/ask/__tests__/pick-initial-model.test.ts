import { describe, expect, it } from "vitest";

import type { Chat, LLMModelInfo } from "@/lib/api-types";

import { pickInitialModel } from "../pick-initial-model";

const model = (id: string): LLMModelInfo => ({
  id,
  provider: id.split(":")[0],
  bare_id: id.split(":")[1] ?? id,
  display_name: id,
  context_window: 200_000,
  supports_citations: false,
  supports_tools: false,
  supports_vision: false,
  supports_caching: false,
  input_cost_per_mtok: 0,
  output_cost_per_mtok: 0,
  typical_ttft_ms: 0,
});

const chat = (defaultModel: string): Chat => ({
  id: "c",
  user_id: "u",
  title: "",
  default_model: defaultModel,
  created_at: "",
  updated_at: "",
});

const models = [model("anthropic:claude-sonnet-4-6"), model("openai:gpt-5")];

describe("pickInitialModel", () => {
  it("prefers chat.default_model when set", () => {
    expect(pickInitialModel(chat("openai:gpt-5"), models, "anthropic:claude-sonnet-4-6"))
      .toBe("openai:gpt-5");
  });

  it("falls back to localStorage when chat default empty and lastUsed valid", () => {
    expect(pickInitialModel(chat(""), models, "openai:gpt-5"))
      .toBe("openai:gpt-5");
  });

  it("ignores localStorage value not in the visible list", () => {
    expect(pickInitialModel(chat(""), models, "ollama:something"))
      .toBe("anthropic:claude-sonnet-4-6");
  });

  it("falls back to the first visible model", () => {
    expect(pickInitialModel(undefined, models, null))
      .toBe("anthropic:claude-sonnet-4-6");
  });

  it("returns empty string when nothing available", () => {
    expect(pickInitialModel(undefined, [], null)).toBe("");
  });
});
