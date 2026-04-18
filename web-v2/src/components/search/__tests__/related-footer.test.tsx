import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import { server } from "@/test/mocks/server";
import { RelatedFooter } from "../related-footer";

describe("RelatedFooter", () => {
  it("is collapsed by default, shows the count, and fetches only on expand", async () => {
    let requests = 0;
    server.use(
      http.get("*/api/documents/:id/related", () => {
        requests++;
        return HttpResponse.json({
          data: { outgoing: [], incoming: [] },
        });
      }),
    );
    renderWithRouter(
      <RelatedFooter docID="d-email-1" count={3} onNavigate={() => {}} />,
    );

    await waitFor(() => {
      expect(screen.getByText("related")).toBeInTheDocument();
    });
    expect(screen.getByText("(3)")).toBeInTheDocument();
    expect(requests).toBe(0);

    await userEvent.setup().click(screen.getByText("related"));
    await waitFor(() => {
      expect(requests).toBe(1);
    });
  });

  it("renders incoming edges grouped by relation type with human labels", async () => {
    const onNavigate = vi.fn();
    renderWithRouter(
      <RelatedFooter docID="d-email-1" count={1} onNavigate={onNavigate} />,
    );

    await waitFor(() => {
      expect(screen.getByText("related")).toBeInTheDocument();
    });
    await userEvent.setup().click(screen.getByText("related"));

    // Default MSW handler returns one attachment_of edge pointing at
    // invoice.pdf for doc id d-email-1. Should render as "Referenced by"
    // section with "Attachments (1)" group.
    await waitFor(() => {
      expect(screen.getByText("Referenced by")).toBeInTheDocument();
    });
    expect(screen.getByText("Attachments")).toBeInTheDocument();
    // "(1)" appears twice: once in the toggle count, once in the group label.
    expect(screen.getAllByText("(1)").length).toBeGreaterThanOrEqual(2);

    await userEvent.setup().click(
      screen.getByRole("button", { name: "invoice.pdf" }),
    );
    expect(onNavigate).toHaveBeenCalledWith(
      expect.objectContaining({ title: "invoice.pdf" }),
    );
  });

  it("renders outgoing edges with human prefix labels", async () => {
    server.use(
      http.get("*/api/documents/:id/related", () =>
        HttpResponse.json({
          data: {
            outgoing: [
              {
                relation: { type: "reply_to", target_source_id: "INBOX:100" },
                document: {
                  id: "d-parent",
                  source_type: "imap",
                  source_name: "mail",
                  source_id: "INBOX:100",
                  title: "Parent email",
                  content: "",
                  visibility: "private",
                  created_at: "2026-04-10T00:00:00Z",
                  indexed_at: "2026-04-10T00:00:00Z",
                },
              },
            ],
            incoming: [],
          },
        }),
      ),
    );
    renderWithRouter(
      <RelatedFooter docID="d-reply" count={1} onNavigate={() => {}} />,
    );

    await waitFor(() => {
      expect(screen.getByText("related")).toBeInTheDocument();
    });
    await userEvent.setup().click(screen.getByText("related"));

    await waitFor(() => {
      expect(screen.getByText("Points to")).toBeInTheDocument();
    });
    expect(screen.getByText("Reply to:")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Parent email" }),
    ).toBeInTheDocument();
  });

  it("falls back to source_id for dangling edges", async () => {
    server.use(
      http.get("*/api/documents/:id/related", () =>
        HttpResponse.json({
          data: {
            outgoing: [
              {
                relation: {
                  type: "reply_to",
                  target_source_id: "INBOX:missing-parent",
                },
              },
            ],
            incoming: [],
          },
        }),
      ),
    );
    renderWithRouter(
      <RelatedFooter docID="d-email-2" count={1} onNavigate={() => {}} />,
    );

    await waitFor(() => {
      expect(screen.getByText("related")).toBeInTheDocument();
    });
    await userEvent.setup().click(screen.getByText("related"));
    await waitFor(() => {
      expect(screen.getByText("INBOX:missing-parent")).toBeInTheDocument();
    });
  });
});
