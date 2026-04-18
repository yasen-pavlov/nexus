import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render as renderWithProviders } from "@/test/test-utils";
import type { DocumentHit } from "@/lib/api-types";
import { EmailCardBody } from "../cards/email";
import { TelegramCardBody } from "../cards/telegram";
import { PaperlessCardBody } from "../cards/paperless";
import { FilesystemCardBody } from "../cards/filesystem";
import { DefaultCardBody } from "../cards/default";

function baseHit(overrides: Partial<DocumentHit>): DocumentHit {
  return {
    id: "d1",
    source_type: "imap",
    source_name: "mail",
    source_id: "sid",
    title: "t",
    content: "c",
    visibility: "private",
    created_at: "2026-04-17T00:00:00Z",
    indexed_at: "2026-04-17T00:00:00Z",
    rank: 1,
    ...overrides,
  };
}

describe("EmailCardBody", () => {
  it("renders from/to with arrow", () => {
    render(
      <EmailCardBody
        hit={baseHit({
          metadata: {
            from: "Alice <alice@example.com>",
            to: "bob@example.com",
          },
        })}
      />,
    );
    expect(screen.getByText("Alice <alice@example.com>")).toBeInTheDocument();
    expect(screen.getByText("bob@example.com")).toBeInTheDocument();
    expect(screen.getByText("→")).toBeInTheDocument();
  });

  it("renders attachment chips with filename", () => {
    render(
      <EmailCardBody
        hit={baseHit({
          metadata: {
            from: "a@b.com",
            has_attachments: true,
            attachment_filenames: ["invoice.pdf", "photo.jpg"],
          },
        })}
      />,
    );
    expect(screen.getByText("invoice.pdf")).toBeInTheDocument();
    expect(screen.getByText("photo.jpg")).toBeInTheDocument();
  });

  it("hides INBOX folder badge but shows custom folders", () => {
    const { rerender } = render(
      <EmailCardBody
        hit={baseHit({ metadata: { from: "a@b.com", folder: "INBOX" } })}
      />,
    );
    expect(screen.queryByText("INBOX")).not.toBeInTheDocument();
    rerender(
      <EmailCardBody
        hit={baseHit({ metadata: { from: "a@b.com", folder: "Sent" } })}
      />,
    );
    expect(screen.getByText("Sent")).toBeInTheDocument();
  });
});

describe("TelegramCardBody", () => {
  it("shows chat name and message count", () => {
    render(
      <TelegramCardBody
        hit={baseHit({
          source_type: "telegram",
          metadata: { chat_name: "Family", message_count: 21 },
          conversation_id: "12345",
        })}
      />,
    );
    expect(screen.getByText("Family")).toBeInTheDocument();
    expect(screen.getByText(/21 messages/)).toBeInTheDocument();
  });

  it("fires onOpenChat when button clicked", async () => {
    const onOpenChat = vi.fn();
    const hit = baseHit({
      source_type: "telegram",
      metadata: { chat_name: "Family" },
      conversation_id: "12345",
    });
    render(<TelegramCardBody hit={hit} onOpenChat={onOpenChat} />);
    await userEvent.setup().click(
      screen.getByRole("button", { name: /open in chat/i }),
    );
    expect(onOpenChat).toHaveBeenCalledWith(hit);
  });

  it("hides open-in-chat button without conversation_id", () => {
    render(
      <TelegramCardBody
        hit={baseHit({
          source_type: "telegram",
          metadata: { chat_name: "Family" },
        })}
        onOpenChat={() => {}}
      />,
    );
    expect(
      screen.queryByRole("button", { name: /open in chat/i }),
    ).not.toBeInTheDocument();
  });

  // Match mode: backend pinpointed the exact matched message inside the
  // window. The card should render a single-message row: sender name,
  // timestamp, highlighted body, and a muted "in <chat>" tail.
  it("renders match mode with sender, timestamp, and highlighted snippet", async () => {
    const hit = baseHit({
      source_type: "telegram",
      source_name: "tg-main",
      conversation_id: "12345",
      headline: "По малкото <mark>пипонче</mark>",
      match_source_id: "12345:228870:msg",
      match_message_id: 228870,
      match_sender_id: 577483548,
      match_sender_name: "Yasen Pavlov",
      match_created_at: "2026-04-08T19:18:37+02:00",
      match_avatar_key: "avatars:577483548",
      metadata: { chat_name: "Family", message_count: 21 },
    });
    renderWithProviders(<TelegramCardBody hit={hit} onOpenChat={() => {}} />);

    expect(screen.getByText("Yasen Pavlov")).toBeInTheDocument();
    // Chat name moves to the muted tail, not the main body.
    expect(screen.getByText(/Family/)).toBeInTheDocument();
    expect(screen.getByText(/21 in thread/)).toBeInTheDocument();
    expect(screen.getByText("пипонче")).toBeInTheDocument();
  });

  // Semantic-fallback mode: no match_source_id, but message_lines has
  // enough data to render a bookended preview + semantic-match pill.
  it("renders semantic mode with bookended messages and semantic-match pill", () => {
    const hit = baseHit({
      source_type: "telegram",
      source_name: "tg-main",
      conversation_id: "12345",
      metadata: {
        chat_name: "Family",
        message_count: 5,
        message_lines: [
          {
            id: 100,
            text: "Zimmer 243",
            created_at: "2026-04-08T16:16:00Z",
            sender_id: 111,
            sender_name: "Maria",
          },
          {
            id: 104,
            text: "Малко става тъмно",
            created_at: "2026-04-08T19:24:00Z",
            sender_id: 222,
            sender_name: "Yasen",
          },
        ],
      },
    });
    renderWithProviders(<TelegramCardBody hit={hit} onOpenChat={() => {}} />);

    expect(screen.getByText("Maria")).toBeInTheDocument();
    expect(screen.getByText("Yasen")).toBeInTheDocument();
    expect(screen.getByText(/Zimmer 243/)).toBeInTheDocument();
    expect(screen.getByText(/Малко става тъмно/)).toBeInTheDocument();
    expect(screen.getByText(/semantic match/i)).toBeInTheDocument();
  });

  it("match mode fires onOpenChat when the button is clicked", async () => {
    const onOpenChat = vi.fn();
    const hit = baseHit({
      source_type: "telegram",
      conversation_id: "12345",
      headline: "<mark>hi</mark>",
      match_source_id: "12345:1:msg",
      match_message_id: 1,
      match_sender_name: "Alice",
      match_created_at: "2026-04-08T18:00:00Z",
      metadata: { chat_name: "Family" },
    });
    renderWithProviders(<TelegramCardBody hit={hit} onOpenChat={onOpenChat} />);
    await userEvent.setup().click(
      screen.getByRole("button", { name: /open in chat/i }),
    );
    expect(onOpenChat).toHaveBeenCalledWith(hit);
  });
});

describe("PaperlessCardBody", () => {
  it("renders correspondent · document_type letterhead with tag pills", () => {
    render(
      <PaperlessCardBody
        hit={baseHit({
          source_type: "paperless",
          mime_type: "application/pdf",
          size: 102400,
          metadata: {
            correspondent: "Hospital",
            document_type: "Invoice",
            tags: ["health", "2026"],
          },
        })}
      />,
    );
    expect(screen.getByText("Hospital")).toBeInTheDocument();
    expect(screen.getByText("Invoice")).toBeInTheDocument();
    expect(screen.getByText("·")).toBeInTheDocument();
    expect(screen.getByText("health")).toBeInTheDocument();
    expect(screen.getByText("2026")).toBeInTheDocument();
    expect(screen.getByText("100 KB")).toBeInTheDocument();
  });

  it("shows download button when mime_type is set", async () => {
    const onDownload = vi.fn();
    const hit = baseHit({
      source_type: "paperless",
      mime_type: "application/pdf",
      metadata: { correspondent: "Hospital" },
    });
    render(<PaperlessCardBody hit={hit} onDownload={onDownload} />);
    await userEvent.setup().click(
      screen.getByRole("button", { name: /download/i }),
    );
    expect(onDownload).toHaveBeenCalledWith(hit);
  });
});

describe("FilesystemCardBody", () => {
  it("renders path with segmented slashes and extension badge", () => {
    render(
      <FilesystemCardBody
        hit={baseHit({
          source_type: "filesystem",
          metadata: {
            path: "notes/work/meeting.md",
            size: 2048,
            extension: ".md",
          },
        })}
      />,
    );
    expect(screen.getByText("notes")).toBeInTheDocument();
    expect(screen.getByText("work")).toBeInTheDocument();
    expect(screen.getByText("meeting.md")).toBeInTheDocument();
    // "md" is styled uppercase via Tailwind — the DOM text is lowercase.
    expect(screen.getByText("md")).toBeInTheDocument();
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
  });
});

describe("DefaultCardBody", () => {
  it("renders key-value grid for unknown source", () => {
    render(
      <DefaultCardBody
        hit={baseHit({
          source_type: "unknown",
          metadata: { foo: "bar", count: 7 },
        })}
      />,
    );
    expect(screen.getByText("foo")).toBeInTheDocument();
    expect(screen.getByText("bar")).toBeInTheDocument();
    expect(screen.getByText("count")).toBeInTheDocument();
    expect(screen.getByText("7")).toBeInTheDocument();
  });

  it("renders nothing when metadata is empty", () => {
    const { container } = render(
      <DefaultCardBody hit={baseHit({ metadata: {} })} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
