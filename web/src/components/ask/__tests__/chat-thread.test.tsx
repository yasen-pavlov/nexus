import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithRouter, screen, waitFor } from "@/test/test-utils";
import type {
  ChatDetailResponse,
  ChatMessage,
  LLMModelInfo,
} from "@/lib/api-types";
import type { StreamingTurn } from "@/hooks/use-chat-stream";

import { ChatThread } from "../chat-thread";

// ChatThread composes several data hooks. Mock them so each test can
// drive a specific render path (pending / not-found / empty / populated
// / streaming) without a real network round-trip. AssistantTurn's own
// feedback hook is stubbed too so persisted assistant turns mount.
const { useChatMock, useChatStreamMock, useLLMModelsMock, useLLMDefaultMock } =
  vi.hoisted(() => ({
    useChatMock: vi.fn(),
    useChatStreamMock: vi.fn(),
    useLLMModelsMock: vi.fn(),
    useLLMDefaultMock: vi.fn(),
  }));

vi.mock("@/hooks/use-chats", () => ({
  useChat: (id: string) => useChatMock(id),
}));
vi.mock("@/hooks/use-chat-stream", () => ({
  useChatStream: (id: string) => useChatStreamMock(id),
}));
vi.mock("@/hooks/use-llm-models", () => ({
  useLLMModels: () => useLLMModelsMock(),
  useLLMDefault: () => useLLMDefaultMock(),
}));
vi.mock("@/hooks/use-message-feedback", () => ({
  useMessageFeedback: () => ({ mutate: vi.fn() }),
}));

const sampleModels: LLMModelInfo[] = [
  {
    id: "anthropic:claude-sonnet-4-6",
    provider: "anthropic",
    bare_id: "claude-sonnet-4-6",
    display_name: "claude-sonnet-4-6",
    context_window: 200_000,
    supports_citations: true,
    supports_tools: true,
    supports_vision: false,
    supports_caching: true,
    input_cost_per_mtok: 3.0,
    output_cost_per_mtok: 15.0,
    typical_ttft_ms: 800,
  },
];

const IDLE_TURN = {
  phase: "idle",
  userContent: "",
  evidence: [],
  answer: "",
  citations: [],
  toolEvents: [],
} as unknown as StreamingTurn;

function detail(messages: ChatMessage[] = []): ChatDetailResponse {
  return {
    chat: {
      id: "chat-1",
      user_id: "user-1",
      title: "Anthropic invoices",
      default_model: "anthropic:claude-sonnet-4-6",
      created_at: "2026-06-03T00:00:00Z",
      updated_at: "2026-06-03T00:00:00Z",
    },
    messages,
  };
}

function userMsg(overrides?: Partial<ChatMessage>): ChatMessage {
  return {
    id: "u1",
    chat_id: "chat-1",
    role: "user",
    seq: 0,
    content: "What did the Anthropic invoices total?",
    created_at: "2026-06-03T00:00:00Z",
    ...overrides,
  };
}

function assistantMsg(overrides?: Partial<ChatMessage>): ChatMessage {
  return {
    id: "a1",
    chat_id: "chat-1",
    role: "assistant",
    seq: 1,
    content: "They totalled €120.",
    created_at: "2026-06-03T00:00:01Z",
    ...overrides,
  };
}

function setChat(value: Record<string, unknown>) {
  useChatMock.mockReturnValue(value);
}

beforeEach(() => {
  // Sensible defaults: models + default loaded, no active stream.
  useLLMModelsMock.mockReturnValue({ data: sampleModels });
  useLLMDefaultMock.mockReturnValue({
    data: { default_model: "anthropic:claude-sonnet-4-6" },
  });
  useChatStreamMock.mockReturnValue({
    turn: IDLE_TURN,
    start: vi.fn(),
    cancel: vi.fn(),
    reset: vi.fn(),
  });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("ChatThread", () => {
  it("renders a skeleton while the chat detail is pending", async () => {
    setChat({ isPending: true, isError: false, data: undefined });
    const { container } = renderWithRouter(<ChatThread chatID="chat-1" />);
    await waitFor(() =>
      expect(container.querySelector('[data-slot="skeleton"]')).not.toBeNull(),
    );
    // No composer rendered in the skeleton path.
    expect(screen.queryByPlaceholderText(/Ask anything/)).not.toBeInTheDocument();
  });

  it("renders the not-found state on error", async () => {
    setChat({ isPending: false, isError: true, data: undefined });
    renderWithRouter(<ChatThread chatID="chat-1" />, {
      extraRoutes: ["/ask"],
    });
    await waitFor(() =>
      expect(screen.getByText("Chat not found")).toBeInTheDocument(),
    );
    expect(screen.getByRole("link", { name: /Back to Ask/ })).toBeInTheDocument();
  });

  it("renders the composer with an empty (first-turn) thread", async () => {
    setChat({ isPending: false, isError: false, data: detail([]) });
    renderWithRouter(<ChatThread chatID="chat-1" />);
    // Empty thread → first-turn composer ("Ask a follow-up…" placeholder).
    await waitFor(() =>
      expect(
        screen.getByPlaceholderText(/Ask a follow-up/),
      ).toBeInTheDocument(),
    );
    // No persisted turns: nothing from the message list rendered.
    expect(
      screen.queryByText("They totalled €120."),
    ).not.toBeInTheDocument();
  });

  it("renders persisted user + assistant turns from the thread", async () => {
    setChat({
      isPending: false,
      isError: false,
      data: detail([userMsg(), assistantMsg()]),
    });
    renderWithRouter(<ChatThread chatID="chat-1" />);
    await waitFor(() =>
      expect(
        screen.getByText("What did the Anthropic invoices total?"),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("They totalled €120.")).toBeInTheDocument();
  });

  it("renders the streaming turn (phase chip + synthetic user bubble) while a turn is active", async () => {
    setChat({ isPending: false, isError: false, data: detail([]) });
    useChatStreamMock.mockReturnValue({
      turn: {
        ...IDLE_TURN,
        phase: "streaming",
        userContent: "Live question?",
        answer: "Partial answer…",
        startedAt: Date.now(),
      },
      start: vi.fn(),
      cancel: vi.fn(),
      reset: vi.fn(),
    });
    renderWithRouter(<ChatThread chatID="chat-1" />);
    // Synthetic user bubble echoes the in-flight question.
    await waitFor(() =>
      expect(screen.getByText("Live question?")).toBeInTheDocument(),
    );
    // The streaming answer body renders.
    expect(screen.getByText(/Partial answer/)).toBeInTheDocument();
    // PhaseChip shows the generating label during streaming.
    expect(screen.getByText("Generating answer")).toBeInTheDocument();
  });

  it("hides the persisted twin (structural: assistant by id + preceding user)", async () => {
    // A finished turn whose twin IS persisted (messageID set). The persisted
    // user+assistant pair must be hidden so only the streaming card shows it.
    setChat({
      isPending: false,
      isError: false,
      data: detail([
        userMsg({ id: "u1", seq: 0, content: "Dup question" }),
        assistantMsg({ id: "a1", seq: 1 }),
      ]),
    });
    useChatStreamMock.mockReturnValue({
      turn: {
        ...IDLE_TURN,
        phase: "done",
        messageID: "a1",
        userContent: "Dup question",
        answer: "answer",
        startedAt: Date.now(),
      },
      start: vi.fn(),
      cancel: vi.fn(),
      reset: vi.fn(),
    });
    renderWithRouter(<ChatThread chatID="chat-1" />);
    await waitFor(() =>
      expect(screen.getAllByText("Dup question")).toHaveLength(1),
    );
  });

  it("does NOT hide an earlier identical user bubble when the message repeats", async () => {
    // Two turns both with user content "yes". Only the just-persisted twin
    // (messageID a2 + its preceding user u2) is hidden — the earlier "yes"
    // bubble and its answer must survive.
    setChat({
      isPending: false,
      isError: false,
      data: detail([
        userMsg({ id: "u0", seq: 0, content: "yes" }),
        assistantMsg({ id: "a0", seq: 1, content: "first answer" }),
        userMsg({ id: "u2", seq: 2, content: "yes" }),
        assistantMsg({ id: "a2", seq: 3, content: "second answer" }),
      ]),
    });
    useChatStreamMock.mockReturnValue({
      turn: {
        ...IDLE_TURN,
        phase: "done",
        messageID: "a2",
        userContent: "yes",
        answer: "streamed",
        startedAt: Date.now(),
      },
      start: vi.fn(),
      cancel: vi.fn(),
      reset: vi.fn(),
    });
    renderWithRouter(<ChatThread chatID="chat-1" />);
    // Earlier turn survives.
    await waitFor(() =>
      expect(screen.getByText("first answer")).toBeInTheDocument(),
    );
    // Two "yes": the earlier persisted u0 + the streaming card. u2 (twin) hidden.
    expect(screen.getAllByText("yes")).toHaveLength(2);
    // The just-persisted assistant twin is hidden (streaming card shows it).
    expect(screen.queryByText("second answer")).not.toBeInTheDocument();
  });
});
