import { describe, expect, it, vi } from "vitest";
import { fireEvent } from "@testing-library/react";

import { render, screen } from "@/test/test-utils";
import { AskComposer } from "../ask-composer";
import type { LLMModelInfo } from "@/lib/api-types";

const models: LLMModelInfo[] = [
  {
    id: "anthropic:claude-sonnet-4-6",
    provider: "anthropic",
    bare_id: "claude-sonnet-4-6",
    display_name: "Claude Sonnet 4.6",
    context_window: 1_000_000,
    supports_citations: true,
    supports_tools: true,
    supports_vision: true,
    supports_caching: true,
    input_cost_per_mtok: 3,
    output_cost_per_mtok: 15,
    typical_ttft_ms: 700,
  },
];

function baseProps() {
  return {
    model: "anthropic:claude-sonnet-4-6",
    onModelChange: vi.fn(),
    models,
    onSubmit: vi.fn(),
  };
}

describe("AskComposer", () => {
  it("shows the ask-anything placeholder on an empty (first-turn) thread", () => {
    render(<AskComposer {...baseProps()} isFirstTurn />);
    expect(
      screen.getByPlaceholderText(/Ask anything/),
    ).toBeInTheDocument();
  });

  it("shows the follow-up placeholder on a non-empty thread", () => {
    render(<AskComposer {...baseProps()} isFirstTurn={false} />);
    expect(
      screen.getByPlaceholderText(/Ask a follow-up/),
    ).toBeInTheDocument();
  });

  it("uses readOnly (not disabled) while streaming so Esc-to-cancel still fires", () => {
    const onCancel = vi.fn();
    render(
      <AskComposer
        {...baseProps()}
        isStreaming
        onCancel={onCancel}
        initialContent="hello"
      />,
    );
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    // A disabled textarea receives no key events; readOnly keeps focus + keydown.
    expect(textarea).toHaveAttribute("readonly");
    expect(textarea).not.toBeDisabled();

    fireEvent.keyDown(textarea, { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
