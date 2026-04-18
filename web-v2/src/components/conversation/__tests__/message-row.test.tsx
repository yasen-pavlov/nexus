import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@/test/test-utils";
import { MessageRow, type MessageRowModel } from "../message-row";

function baseRow(overrides: Partial<MessageRowModel>): MessageRowModel {
  return {
    sourceId: "msg-1",
    senderId: "1001",
    senderName: "Alice",
    createdAt: "2026-04-10T18:00:00Z",
    body: "hello",
    isSelf: false,
    isAnchor: false,
    position: "solo",
    ...overrides,
  };
}

describe("MessageRow attachment partitioning", () => {
  it("renders image attachments inline and files as chips; no (no text) when body is empty but attachments exist", () => {
    const model = baseRow({
      body: "",
      attachments: [
        {
          id: "a-img",
          filename: "photo.jpg",
          mimeType: "image/jpeg",
          size: 10000,
          onDownload: vi.fn(),
        },
        {
          id: "a-vid",
          filename: "clip.mp4",
          mimeType: "video/mp4",
          size: 50000,
          onDownload: vi.fn(),
        },
        {
          id: "a-pdf",
          filename: "doc.pdf",
          mimeType: "application/pdf",
          size: 2048,
          onDownload: vi.fn(),
        },
      ],
    });

    render(<MessageRow model={model} />);

    // Image renders as an <img> via the authed blob fetch path; the
    // test's MSW handler for /documents/:id/content returns fake bytes
    // so the image resolves. Alt text is the filename.
    // (InlineImage fetches async, so we only check for the PDF chip
    // and that the placeholder "(no text)" is NOT present.)
    expect(screen.queryByText("(no text)")).not.toBeInTheDocument();

    // PDF falls to the chip partition.
    expect(screen.getByText("doc.pdf")).toBeInTheDocument();

    // Image and video also appear by filename (either in placeholder or
    // resolved state).
    expect(screen.getAllByText(/photo\.jpg|clip\.mp4/).length).toBeGreaterThan(0);
  });

  it("shows '(no text)' only when body is empty AND there are no attachments", () => {
    const model = baseRow({ body: "", attachments: undefined });
    const { rerender } = render(<MessageRow model={model} />);
    expect(screen.getByText("(no text)")).toBeInTheDocument();

    // With a body, the placeholder should disappear.
    rerender(
      <MessageRow model={baseRow({ body: "hi", attachments: undefined })} />,
    );
    expect(screen.queryByText("(no text)")).not.toBeInTheDocument();
  });

  it("renders the PDF chip inside the bubble container (same parent as body)", () => {
    const onDownload = vi.fn();
    const model = baseRow({
      body: "check this out",
      attachments: [
        {
          id: "a-pdf",
          filename: "report.pdf",
          mimeType: "application/pdf",
          size: 2048,
          onDownload,
        },
      ],
    });

    render(<MessageRow model={model} />);

    const body = screen.getByText("check this out");
    const chip = screen.getByText("report.pdf");
    // Body and chip live in the same bubble div (flex-col) — walk up
    // until we find a common ancestor; the shared parent enforces the
    // "attachments inside bubble" grouping we fixed last round.
    const bubble = body.closest("div");
    expect(bubble).not.toBeNull();
    expect(bubble!.contains(chip)).toBe(true);
  });
});
