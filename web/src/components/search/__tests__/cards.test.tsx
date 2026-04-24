import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render as renderWithProviders } from "@/test/test-utils";
import type { DocumentHit } from "@/lib/api-types";
import { EmailCardBody } from "../cards/email";
import { AttachmentCardBody } from "../cards/attachment";
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
  it("renders parsed sender name + address and recipient routing", () => {
    render(
      <EmailCardBody
        hit={baseHit({
          metadata: {
            from: "Alice Smith <alice@example.com>",
            to: "bob@example.com",
          },
        })}
      />,
    );
    expect(screen.getByText("Alice Smith")).toBeInTheDocument();
    expect(screen.getByText("alice@example.com")).toBeInTheDocument();
    expect(screen.getByText("bob@example.com")).toBeInTheDocument();
    expect(screen.getByText("→")).toBeInTheDocument();
  });

  it("shows +N badge when there are multiple recipients", () => {
    render(
      <EmailCardBody
        hit={baseHit({
          metadata: {
            from: "Alice <alice@example.com>",
            to: "bob@example.com, carol@example.com, dave@example.com",
          },
        })}
      />,
    );
    expect(screen.getByText("+2")).toBeInTheDocument();
  });

  it("shows cc badge when cc is present", () => {
    render(
      <EmailCardBody
        hit={baseHit({
          metadata: {
            from: "a@b.com",
            to: "c@d.com",
            cc: "e@f.com",
          },
        })}
      />,
    );
    expect(screen.getByText("cc")).toBeInTheDocument();
  });

  it("renders attachment chips with filenames", () => {
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

  it("renders clickable attachment chips when `attachments` metadata has ids", async () => {
    const onAttachmentClick = vi.fn();
    render(
      <EmailCardBody
        hit={baseHit({
          metadata: {
            from: "a@b.com",
            attachments: [
              { id: "att-doc-1", filename: "invoice.pdf" },
              { id: "att-doc-2", filename: "photo.jpg" },
            ],
          },
        })}
        onAttachmentClick={onAttachmentClick}
      />,
    );
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: /invoice\.pdf/i }));
    expect(onAttachmentClick).toHaveBeenCalledWith({
      id: "att-doc-1",
      filename: "invoice.pdf",
    });
  });

  it("falls back to static chips when only legacy `attachment_filenames` is present", () => {
    const onAttachmentClick = vi.fn();
    render(
      <EmailCardBody
        hit={baseHit({
          metadata: {
            from: "a@b.com",
            attachment_filenames: ["legacy.pdf"],
          },
        })}
        onAttachmentClick={onAttachmentClick}
      />,
    );
    // No button, just a static span — legacy docs can't resolve to an ID.
    expect(
      screen.queryByRole("button", { name: /legacy\.pdf/i }),
    ).toBeNull();
    expect(screen.getByText("legacy.pdf")).toBeInTheDocument();
  });

  it("falls back to a 'has attachments' pill when only the flag is set", () => {
    render(
      <EmailCardBody
        hit={baseHit({
          metadata: {
            from: "a@b.com",
            has_attachments: true,
          },
        })}
      />,
    );
    expect(screen.getByText("has attachments")).toBeInTheDocument();
  });

  it("hides INBOX folder but shows custom folders", () => {
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

  it("renders highlighted snippet inside the excerpt block", () => {
    const { container } = render(
      <EmailCardBody
        hit={baseHit({
          metadata: { from: "a@b.com" },
          headline: "hello <em>match</em>",
        })}
      />,
    );
    expect(container.querySelector("em")?.textContent).toBe("match");
  });

  it("returns null when the hit has nothing useful to show", () => {
    const { container } = render(
      <EmailCardBody hit={baseHit({ content: "", metadata: {} })} />,
    );
    expect(container.firstChild).toBeNull();
  });
});

describe("AttachmentCardBody", () => {
  function attachmentHit(overrides: Partial<DocumentHit> = {}): DocumentHit {
    return baseHit({
      title: "invoice.pdf",
      mime_type: "application/pdf",
      size: 35041,
      metadata: {
        parent_subject: "Your bill for April",
        filename: "invoice.pdf",
        content_type: "application/pdf",
      },
      headline: "Total amount <em>due</em>: $42",
      ...overrides,
    });
  }

  it("renders provenance line with parent subject", () => {
    render(<AttachmentCardBody hit={attachmentHit()} />);
    expect(screen.getByText("Your bill for April")).toBeInTheDocument();
    expect(screen.getByText(/attached to/i)).toBeInTheDocument();
  });

  it("renders the mime stamp on the document tile", () => {
    render(<AttachmentCardBody hit={attachmentHit()} />);
    expect(screen.getByText("PDF")).toBeInTheDocument();
  });

  it("falls back to extension when mime is octet-stream", () => {
    render(
      <AttachmentCardBody
        hit={attachmentHit({
          mime_type: "application/octet-stream",
          title: "bundle.tar.gz",
          metadata: {
            parent_subject: "archive",
            filename: "bundle.tar.gz",
            content_type: "application/octet-stream",
          },
        })}
      />,
    );
    expect(screen.getByText("GZ")).toBeInTheDocument();
  });

  it("renders the extracted snippet inside the content box", () => {
    const { container } = render(<AttachmentCardBody hit={attachmentHit()} />);
    expect(container.querySelector("em")?.textContent).toBe("due");
  });

  it("renders size in the meta row", () => {
    render(<AttachmentCardBody hit={attachmentHit()} />);
    expect(screen.getByText(/KB|MB|bytes|B$/)).toBeInTheDocument();
  });

  it("fires onDownload when the document tile is clicked", async () => {
    const onDownload = vi.fn();
    const hit = attachmentHit();
    render(<AttachmentCardBody hit={hit} onDownload={onDownload} />);
    // The tile itself is the download affordance — no separate button.
    // Accessible name comes from the mime stamp ("PDF") text content.
    await userEvent
      .setup()
      .click(screen.getByTitle(/download invoice\.pdf/i));
    expect(onDownload).toHaveBeenCalledWith(hit);
  });

  it("renders a plain tile (no button) when no download handler is passed", () => {
    render(<AttachmentCardBody hit={attachmentHit()} />);
    expect(screen.queryByRole("button")).toBeNull();
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
  it("renders letterhead with correspondent, document type stamp, and tag pills", () => {
    render(
      <PaperlessCardBody
        hit={baseHit({
          source_type: "paperless",
          mime_type: "application/pdf",
          size: 102400,
          metadata: {
            correspondent: "Hospital GmbH",
            document_type: "Invoice",
            tags: ["health", "2026"],
            added: "2026-04-20T00:00:00Z",
          },
        })}
      />,
    );
    expect(screen.getByText("Hospital GmbH")).toBeInTheDocument();
    expect(screen.getByText(/correspondent/i)).toBeInTheDocument();
    expect(screen.getByText("Invoice")).toBeInTheDocument();
    expect(screen.getByText("health")).toBeInTheDocument();
    expect(screen.getByText("2026")).toBeInTheDocument();
    expect(screen.getByText("100 KB")).toBeInTheDocument();
    expect(screen.getByText(/filed/i)).toBeInTheDocument();
  });

  it("hides letterhead when correspondent is missing", () => {
    render(
      <PaperlessCardBody
        hit={baseHit({
          source_type: "paperless",
          mime_type: "application/pdf",
          metadata: { tags: ["misc"] },
        })}
      />,
    );
    expect(screen.queryByText(/correspondent/i)).toBeNull();
    expect(screen.getByText("misc")).toBeInTheDocument();
  });

  it("fires onDownload when the Download button is clicked", async () => {
    const onDownload = vi.fn();
    const hit = baseHit({
      source_type: "paperless",
      mime_type: "application/pdf",
      metadata: { correspondent: "Hospital" },
    });
    render(<PaperlessCardBody hit={hit} onDownload={onDownload} />);
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: /download/i }));
    expect(onDownload).toHaveBeenCalledWith(hit);
  });

  it("hides Download button when no handler is passed", () => {
    render(
      <PaperlessCardBody
        hit={baseHit({
          source_type: "paperless",
          metadata: { correspondent: "Hospital" },
        })}
      />,
    );
    expect(screen.queryByRole("button", { name: /download/i })).toBeNull();
  });
});

describe("FilesystemCardBody", () => {
  it("renders monospace path with segmented parent dirs and bold filename", () => {
    render(
      <FilesystemCardBody
        hit={baseHit({
          source_type: "filesystem",
          mime_type: "text/markdown",
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
  });

  it("renders extension stamp in uppercase (DOM text is the raw key)", () => {
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
    expect(screen.getByText("MD")).toBeInTheDocument();
  });

  it("renders size and relative modified time in the meta row", () => {
    render(
      <FilesystemCardBody
        hit={baseHit({
          source_type: "filesystem",
          created_at: new Date().toISOString(),
          metadata: {
            path: "a.md",
            size: 2048,
            extension: ".md",
          },
        })}
      />,
    );
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
    expect(screen.getByText(/today|ago|yesterday/)).toBeInTheDocument();
  });

  it("fires onDownload when the Download button is clicked", async () => {
    const onDownload = vi.fn();
    const hit = baseHit({
      source_type: "filesystem",
      metadata: { path: "x.md", size: 100, extension: ".md" },
    });
    render(<FilesystemCardBody hit={hit} onDownload={onDownload} />);
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: /download/i }));
    expect(onDownload).toHaveBeenCalledWith(hit);
  });

  it("hides Download button when no handler is passed", () => {
    render(
      <FilesystemCardBody
        hit={baseHit({
          source_type: "filesystem",
          metadata: { path: "x.md", extension: ".md" },
        })}
      />,
    );
    expect(screen.queryByRole("button", { name: /download/i })).toBeNull();
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
