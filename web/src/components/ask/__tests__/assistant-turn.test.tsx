import { describe, expect, it, vi } from "vitest";

import { render, screen, userEvent } from "@/test/test-utils";
import type { ChatMessage } from "@/lib/api-types";

import { AssistantTurn } from "../assistant-turn";

const { mockMutate } = vi.hoisted(() => ({ mockMutate: vi.fn() }));
vi.mock("@/hooks/use-message-feedback", () => ({
  useMessageFeedback: () => ({ mutate: mockMutate }),
}));

function persistedMessage(overrides?: Partial<ChatMessage>): ChatMessage {
  return {
    id: "m1",
    chat_id: "c1",
    role: "assistant",
    seq: 2,
    content: "Here is the answer.",
    created_at: "2026-06-03T00:00:00Z",
    ...overrides,
  };
}

describe("AssistantTurn feedback", () => {
  it("rates up on click and toggles off when clicked again", async () => {
    const user = userEvent.setup();
    mockMutate.mockClear();
    render(<AssistantTurn message={persistedMessage()} evidence={[]} />);

    const up = screen.getByRole("button", { name: "Good answer" });
    expect(up).toHaveAttribute("aria-pressed", "false");

    await user.click(up);
    expect(up).toHaveAttribute("aria-pressed", "true");
    expect(mockMutate).toHaveBeenLastCalledWith(
      expect.objectContaining({ chatId: "c1", messageId: "m1", feedback: "up" }),
      expect.anything(),
    );

    await user.click(up); // toggle off → clears
    expect(mockMutate).toHaveBeenLastCalledWith(
      expect.objectContaining({ feedback: null }),
      expect.anything(),
    );
  });

  it("reflects a persisted down rating and rates down", async () => {
    const user = userEvent.setup();
    mockMutate.mockClear();
    render(<AssistantTurn message={persistedMessage({ feedback: "down" })} evidence={[]} />);

    expect(screen.getByRole("button", { name: "Bad answer" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    // Switch to up.
    await user.click(screen.getByRole("button", { name: "Good answer" }));
    expect(mockMutate).toHaveBeenLastCalledWith(
      expect.objectContaining({ feedback: "up" }),
      expect.anything(),
    );
  });

  it("shows no thumbs while streaming (no persisted message)", () => {
    render(
      <AssistantTurn
        streaming={{
          phase: "streaming",
          answer: "partial",
          citations: [],
          evidence: [],
          toolEvents: [],
          userContent: "q",
        } as never}
        evidence={[]}
      />,
    );
    expect(screen.queryByRole("button", { name: "Good answer" })).not.toBeInTheDocument();
  });
});
