import { describe, expect, it, vi } from "vitest";

import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import type { ChatListEntry } from "@/lib/api-types";

import { RecentChatItem } from "../recent-chat-item";

const chat: ChatListEntry = {
  id: "11111111-2222-3333-4444-555555555555",
  title: "Anthropic invoices",
  first_message_preview: "find invoices",
  updated_at: "2026-06-03T10:00:00Z",
};

function mount() {
  const onDelete = vi.fn(async () => {});
  const onRename = vi.fn(async () => {});
  renderWithRouter(
    <RecentChatItem chat={chat} onDelete={onDelete} onRename={onRename} />,
    { extraRoutes: ["/ask/$chatId"] },
  );
  return { onDelete, onRename };
}

describe("RecentChatItem rename", () => {
  it("opens an input seeded with the title and saves the edited value", async () => {
    const user = userEvent.setup();
    const { onRename } = mount();
    await waitFor(() => expect(screen.getByText("Anthropic invoices")).toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "Rename chat" }));
    const input = screen.getByRole("textbox", { name: "Chat title" });
    expect((input as HTMLInputElement).value).toBe("Anthropic invoices");

    await user.clear(input);
    await user.type(input, "Q2 invoices");
    await user.click(screen.getByRole("button", { name: "Save title" }));

    expect(onRename).toHaveBeenCalledWith(chat.id, "Q2 invoices");
  });

  it("saves on Enter and cancels on Escape (Escape does not call onRename)", async () => {
    const user = userEvent.setup();
    const { onRename } = mount();
    await waitFor(() => expect(screen.getByText("Anthropic invoices")).toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "Rename chat" }));
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("textbox", { name: "Chat title" })).not.toBeInTheDocument();
    expect(onRename).not.toHaveBeenCalled();
  });

  it("does not call onRename when the title is unchanged or blank", async () => {
    const user = userEvent.setup();
    const { onRename } = mount();
    await waitFor(() => expect(screen.getByText("Anthropic invoices")).toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "Rename chat" }));
    await user.clear(screen.getByRole("textbox", { name: "Chat title" }));
    await user.click(screen.getByRole("button", { name: "Save title" }));
    expect(onRename).not.toHaveBeenCalled();
  });

  it("confirms and calls onDelete", async () => {
    const user = userEvent.setup();
    const { onDelete } = mount();
    await waitFor(() => expect(screen.getByText("Anthropic invoices")).toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "Delete chat" }));
    await user.click(screen.getByRole("button", { name: "Confirm delete" }));
    expect(onDelete).toHaveBeenCalledWith(chat.id);
  });
});
