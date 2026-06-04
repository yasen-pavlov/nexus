import { describe, expect, it, vi } from "vitest";

import { render, screen, userEvent, waitFor } from "@/test/test-utils";
import type { LLMModelInfo } from "@/lib/api-types";
import { ModelPickerChip } from "../model-picker-chip";

function model(over: Partial<LLMModelInfo>): LLMModelInfo {
  return {
    id: "anthropic:claude-sonnet-4-6",
    provider: "anthropic",
    bare_id: "claude-sonnet-4-6",
    display_name: "Claude Sonnet 4.6",
    context_window: 200_000,
    supports_citations: true,
    supports_tools: true,
    supports_vision: true,
    supports_caching: true,
    input_cost_per_mtok: 3,
    output_cost_per_mtok: 15,
    typical_ttft_ms: 850,
    ...over,
  };
}

const models: LLMModelInfo[] = [
  model({}),
  model({
    id: "openai:gpt-5",
    provider: "openai",
    bare_id: "gpt-5",
    display_name: "GPT-5",
    typical_ttft_ms: 18_000,
    supports_vision: false,
    supports_citations: false,
    supports_caching: false,
  }),
];

describe("ModelPickerChip", () => {
  it("renders the selected model's display name on the chip", () => {
    render(
      <ModelPickerChip value="openai:gpt-5" onChange={vi.fn()} models={models} />,
    );
    expect(
      screen.getByRole("button", { name: /Model: GPT-5/ }),
    ).toBeInTheDocument();
  });

  it("falls back to the bare id when the value is not in the catalog", () => {
    render(
      <ModelPickerChip
        value="ollama:llama3.1"
        onChange={vi.fn()}
        models={models}
      />,
    );
    expect(
      screen.getByRole("button", { name: /Model: llama3.1/ }),
    ).toBeInTheDocument();
  });

  it("opens the popover and shows capability pills + ttft for the selection", async () => {
    render(
      <ModelPickerChip
        value="anthropic:claude-sonnet-4-6"
        onChange={vi.fn()}
        models={models}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: /Model:/ }));
    const dialog = await screen.findByRole("dialog", { name: /Pick model/ });
    expect(dialog).toBeInTheDocument();
    // Capability pills derived from the selected model.
    expect(screen.getByText("200k ctx")).toBeInTheDocument();
    expect(screen.getByText("cites")).toBeInTheDocument();
    expect(screen.getByText("vision")).toBeInTheDocument();
    expect(screen.getByText("cached")).toBeInTheDocument();
    // Sub-second ttft renders in ms.
    expect(screen.getByText(/~850ms first token/)).toBeInTheDocument();
  });

  it("renders multi-second ttft rounded to whole seconds", async () => {
    render(
      <ModelPickerChip value="openai:gpt-5" onChange={vi.fn()} models={models} />,
    );
    await userEvent.click(screen.getByRole("button", { name: /Model:/ }));
    await screen.findByRole("dialog", { name: /Pick model/ });
    expect(screen.getByText(/~18s first token/)).toBeInTheDocument();
    // GPT-5 fixture has no vision/cites/caching → those pills are absent.
    expect(screen.queryByText("vision")).not.toBeInTheDocument();
  });

  it("closes the popover via the backdrop", async () => {
    render(
      <ModelPickerChip value="openai:gpt-5" onChange={vi.fn()} models={models} />,
    );
    await userEvent.click(screen.getByRole("button", { name: /Model:/ }));
    await screen.findByRole("dialog", { name: /Pick model/ });
    await userEvent.click(
      screen.getByRole("button", { name: /Close model picker/ }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: /Pick model/ }),
      ).not.toBeInTheDocument(),
    );
  });
});
