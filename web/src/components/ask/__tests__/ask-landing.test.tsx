import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  renderWithRouter,
  screen,
  userEvent,
  waitFor,
} from "@/test/test-utils";
import type { ChatListEntry, LLMModelInfo } from "@/lib/api-types";

import { AskLanding } from "../ask-landing";

// AskLanding pulls in several CRUD hooks. Mock them so each test drives
// a specific render path (loading / empty / populated recent list, and
// the example-pill → composer prefill flow) without real requests.
const {
  useChatsMock,
  useCreateChatMock,
  useDeleteChatMock,
  useUpdateChatMock,
  useLLMModelsMock,
  useLLMDefaultMock,
} = vi.hoisted(() => ({
  useChatsMock: vi.fn(),
  useCreateChatMock: vi.fn(),
  useDeleteChatMock: vi.fn(),
  useUpdateChatMock: vi.fn(),
  useLLMModelsMock: vi.fn(),
  useLLMDefaultMock: vi.fn(),
}));

vi.mock("@/hooks/use-chats", () => ({
  useChats: (limit: number, offset: number) => useChatsMock(limit, offset),
  useCreateChat: () => useCreateChatMock(),
  useDeleteChat: () => useDeleteChatMock(),
  useUpdateChat: () => useUpdateChatMock(),
}));
vi.mock("@/hooks/use-llm-models", () => ({
  useLLMModels: () => useLLMModelsMock(),
  useLLMDefault: () => useLLMDefaultMock(),
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

function chat(overrides?: Partial<ChatListEntry>): ChatListEntry {
  return {
    id: "11111111-2222-3333-4444-555555555555",
    user_id: "99999999-2222-3333-4444-555555555555",
    title: "Anthropic invoices",
    default_model: "",
    first_message_preview: "find invoices",
    created_at: "2026-06-03T09:00:00Z",
    updated_at: "2026-06-03T10:00:00Z",
    ...overrides,
  };
}

function setChats(value: {
  chats?: ChatListEntry[];
  total?: number;
  isLoading?: boolean;
}) {
  useChatsMock.mockReturnValue({
    chats: value.chats ?? [],
    total: value.total ?? (value.chats?.length ?? 0),
    isLoading: value.isLoading ?? false,
    error: null,
  });
}

beforeEach(() => {
  useLLMModelsMock.mockReturnValue({ data: sampleModels });
  useLLMDefaultMock.mockReturnValue({
    data: { default_model: "anthropic:claude-sonnet-4-6" },
  });
  useCreateChatMock.mockReturnValue({
    mutateAsync: vi.fn(),
    isPending: false,
  });
  useDeleteChatMock.mockReturnValue({ mutateAsync: vi.fn() });
  useUpdateChatMock.mockReturnValue({ mutateAsync: vi.fn() });
  setChats({ chats: [] });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("AskLanding", () => {
  it("renders the hero, the example pills, and the composer", async () => {
    renderWithRouter(<AskLanding />);
    await waitFor(() =>
      expect(
        screen.getByText("What can I help you find?"),
      ).toBeInTheDocument(),
    );
    // All four example prompts render as pills.
    expect(
      screen.getByText("Summarise the last week of Anthropic invoices."),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/What did the team agree on Telegram/),
    ).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/Ask anything/)).toBeInTheDocument();
  });

  it("shows the empty-state copy when there are no recent chats", async () => {
    setChats({ chats: [], total: 0 });
    renderWithRouter(<AskLanding />);
    await waitFor(() =>
      expect(
        screen.getByText(/your first one starts here/),
      ).toBeInTheDocument(),
    );
  });

  it("shows skeletons while recent chats load", async () => {
    setChats({ isLoading: true });
    const { container } = renderWithRouter(<AskLanding />);
    await waitFor(() =>
      expect(container.querySelector('[data-slot="skeleton"]')).not.toBeNull(),
    );
    expect(
      screen.queryByText(/your first one starts here/),
    ).not.toBeInTheDocument();
  });

  it("renders the recent-chats grid with a total badge", async () => {
    setChats({ chats: [chat()], total: 1 });
    renderWithRouter(<AskLanding />, { extraRoutes: ["/ask/$chatId"] });
    await waitFor(() =>
      expect(screen.getByText("Anthropic invoices")).toBeInTheDocument(),
    );
    // The header total badge reflects the count.
    expect(screen.getByText("1")).toBeInTheDocument();
  });

  it("shows the 'showing N most recent' note when total exceeds the recent limit", async () => {
    const many = Array.from({ length: 8 }, (_, i) =>
      chat({ id: `chat-${i}`, title: `Chat ${i}` }),
    );
    setChats({ chats: many, total: 25 });
    renderWithRouter(<AskLanding />, { extraRoutes: ["/ask/$chatId"] });
    await waitFor(() =>
      expect(screen.getByText(/Showing 8 most recent/)).toBeInTheDocument(),
    );
  });

  it("prefills the composer textarea when an example pill is clicked", async () => {
    const user = userEvent.setup();
    renderWithRouter(<AskLanding />);
    const pill = await screen.findByText(
      "Summarise the last week of Anthropic invoices.",
    );
    await user.click(pill);
    await waitFor(() => {
      const textarea = screen.getByPlaceholderText(
        /Ask anything/,
      ) as HTMLTextAreaElement;
      expect(textarea.value).toBe(
        "Summarise the last week of Anthropic invoices.",
      );
    });
  });

  it("renders the handoff skeleton while a search-bar handoff chat is being created", async () => {
    useCreateChatMock.mockReturnValue({
      mutateAsync: vi.fn(() => new Promise(() => {})),
      isPending: true,
    });
    renderWithRouter(<AskLanding initialQuery="from the search bar" />);
    await waitFor(() =>
      expect(screen.getByText(/Starting your chat/)).toBeInTheDocument(),
    );
    // The hero is replaced by the handoff skeleton.
    expect(
      screen.queryByText("What can I help you find?"),
    ).not.toBeInTheDocument();
  });
});
