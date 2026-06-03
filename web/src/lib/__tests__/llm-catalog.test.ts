import { describe, expect, it } from "vitest";

import {
  DEFAULT_LLM_BARE_MODEL,
  LLM_BARE_MODELS,
  LLM_PROVIDERS,
  joinModelID,
  splitModelID,
} from "@/lib/llm-catalog";

describe("llm-catalog", () => {
  it("LLM_PROVIDERS has anthropic, openai, ollama", () => {
    const values = LLM_PROVIDERS.map((p) => p.value);
    expect(values).toEqual(
      expect.arrayContaining(["anthropic", "openai", "ollama"]),
    );
  });

  it("LLM_BARE_MODELS has at least one entry per provider", () => {
    expect(LLM_BARE_MODELS.anthropic.length).toBeGreaterThan(0);
    expect(LLM_BARE_MODELS.openai.length).toBeGreaterThan(0);
    expect(LLM_BARE_MODELS.ollama.length).toBeGreaterThan(0);
  });

  it("DEFAULT_LLM_BARE_MODEL maps each provider to a non-empty bare id", () => {
    expect(DEFAULT_LLM_BARE_MODEL.anthropic).not.toBe("");
    expect(DEFAULT_LLM_BARE_MODEL.openai).not.toBe("");
    expect(DEFAULT_LLM_BARE_MODEL.ollama).not.toBe("");
  });

  describe("splitModelID", () => {
    it("splits provider:bare", () => {
      expect(splitModelID("anthropic:claude-sonnet-4-6")).toEqual({
        provider: "anthropic",
        bare: "claude-sonnet-4-6",
      });
    });

    it("preserves colons inside the bare id (Ollama tags)", () => {
      expect(splitModelID("ollama:qwen3:14b")).toEqual({
        provider: "ollama",
        bare: "qwen3:14b",
      });
    });

    it("rejects malformed inputs", () => {
      expect(splitModelID("")).toEqual({ provider: "", bare: "" });
      expect(splitModelID("anthropic")).toEqual({ provider: "", bare: "" });
      expect(splitModelID("anthropic:")).toEqual({ provider: "", bare: "" });
      expect(splitModelID(":foo")).toEqual({ provider: "", bare: "" });
    });

    it("flags an unknown provider with empty provider but preserved bare", () => {
      expect(splitModelID("vertex:gemini-1.5")).toEqual({
        provider: "",
        bare: "vertex:gemini-1.5",
      });
    });
  });

  describe("joinModelID", () => {
    it("rejoins provider + bare", () => {
      expect(joinModelID("anthropic", "claude-sonnet-4-6")).toBe(
        "anthropic:claude-sonnet-4-6",
      );
      expect(joinModelID("ollama", "qwen3:14b")).toBe("ollama:qwen3:14b");
    });
  });
});
