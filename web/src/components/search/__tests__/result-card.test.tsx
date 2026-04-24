import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "@/test/test-utils";
import type { DocumentHit } from "@/lib/api-types";
import { ResultCard } from "../result-card";

function baseHit(overrides: Partial<DocumentHit> = {}): DocumentHit {
  return {
    id: "d1",
    source_type: "imap",
    source_name: "mail",
    source_id: "sid",
    title: "My Title",
    content: "",
    visibility: "private",
    created_at: "2026-04-17T00:00:00Z",
    indexed_at: "2026-04-17T00:00:00Z",
    rank: 1,
    ...overrides,
  };
}

describe("ResultCard", () => {
  it("title becomes a link when hit.url is present and not file://", () => {
    render(
      <ResultCard
        hit={baseHit({ url: "mid:abc@example.com" })}
        onOpenChat={vi.fn()}
        onDownload={vi.fn()}
        onAttachmentDownload={vi.fn()}
        onNavigateRelated={vi.fn()}
      />,
    );
    const anchor = screen.getByRole("link", { name: /My Title/i });
    expect(anchor).toHaveAttribute("href", "mid:abc@example.com");
    expect(anchor).toHaveAttribute("target", "_blank");
  });

  it("title stays a span when hit.url is absent", () => {
    render(
      <ResultCard
        hit={baseHit({})}
        onOpenChat={vi.fn()}
        onDownload={vi.fn()}
        onAttachmentDownload={vi.fn()}
        onNavigateRelated={vi.fn()}
      />,
    );
    expect(screen.queryByRole("link", { name: /My Title/i })).toBeNull();
    expect(screen.getByText("My Title")).toBeInTheDocument();
  });

  it("title stays a span when hit.url is a file:// URI", () => {
    render(
      <ResultCard
        hit={baseHit({ url: "file:///tmp/foo.txt" })}
        onOpenChat={vi.fn()}
        onDownload={vi.fn()}
        onAttachmentDownload={vi.fn()}
        onNavigateRelated={vi.fn()}
      />,
    );
    expect(screen.queryByRole("link", { name: /My Title/i })).toBeNull();
  });

  it("imap attachment hits route to AttachmentCardBody", () => {
    render(
      <ResultCard
        hit={baseHit({
          id: "att-1",
          source_id: "INBOX:42:attachment:0",
          title: "invoice.pdf",
          mime_type: "application/pdf",
          relations: [
            {
              type: "attachment_of",
              target_source_id: "INBOX:42",
              target_id: "parent-doc-id",
            },
          ],
          metadata: {
            filename: "invoice.pdf",
            parent_subject: "Your bill",
            content_type: "application/pdf",
          },
        })}
        onOpenChat={vi.fn()}
        onDownload={vi.fn()}
        onAttachmentDownload={vi.fn()}
        onNavigateRelated={vi.fn()}
      />,
    );
    // AttachmentCardBody renders the provenance line, not the email
    // from/to arrow. Title still renders in the chassis header.
    expect(screen.queryByText("→")).toBeNull();
    expect(screen.getByText("Your bill")).toBeInTheDocument();
    expect(screen.getByText(/attached to/i)).toBeInTheDocument();
    // Title renders in the chassis header (the h3).
    expect(
      screen.getAllByText("invoice.pdf").length,
    ).toBeGreaterThanOrEqual(1);
  });

  it("imap parent hits still route to EmailCardBody", () => {
    render(
      <ResultCard
        hit={baseHit({
          metadata: {
            from: "Alice <a@example.com>",
            to: "bob@example.com",
          },
        })}
        onOpenChat={vi.fn()}
        onDownload={vi.fn()}
        onAttachmentDownload={vi.fn()}
        onNavigateRelated={vi.fn()}
      />,
    );
    expect(screen.getByText("→")).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("a@example.com")).toBeInTheDocument();
  });
});
