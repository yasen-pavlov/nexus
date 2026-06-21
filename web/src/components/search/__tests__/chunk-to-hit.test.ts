import { describe, expect, it } from "vitest";

import { chunkPreviewToHit } from "../chunk-to-hit";
import { cardOwnsSnippet } from "../card-snippet";
import type { ChunkPreview } from "@/lib/api-types";

describe("chunkPreviewToHit", () => {
  it("maps source → source_type and preserves the rich fields", () => {
    const chunk: ChunkPreview = {
      id: "doc-1",
      title: "Invoice",
      source: "paperless",
      date: "2026-04-06",
      headline: "<em>order</em> #42",
      mime_type: "application/pdf",
      source_name: "paperless",
      source_id: "42",
      size: 2048,
      url: "https://paperless.local/documents/42/details",
      metadata: { correspondent: "Acme" },
    };
    const hit = chunkPreviewToHit(chunk);
    expect(hit.source_type).toBe("paperless");
    expect(hit.source_name).toBe("paperless");
    expect(hit.source_id).toBe("42");
    expect(hit.size).toBe(2048);
    expect(hit.url).toBe("https://paperless.local/documents/42/details");
    expect(hit.metadata).toEqual({ correspondent: "Acme" });
    expect(hit.headline).toBe("<em>order</em> #42");
    expect(hit.created_at).toBe("2026-04-06");
    // No raw content shipped; relations empty so isAttachmentHit() is false.
    expect(hit.content).toBe("");
    expect(hit.relations).toEqual([]);
  });

  it("defaults absent optional fields so the cards degrade gracefully", () => {
    const chunk: ChunkPreview = { id: "d", title: "t", source: "telegram" };
    const hit = chunkPreviewToHit(chunk);
    expect(hit.source_name).toBe("");
    expect(hit.source_id).toBe("");
    expect(hit.url).toBeUndefined();
    expect(hit.metadata).toBeUndefined();
    expect(hit.created_at).toBe("");
    // No source_id → no conversation_id to open the chat with.
    expect(hit.conversation_id).toBeUndefined();
  });

  it("derives conversation_id from a Telegram SourceID (chatID:msgRange)", () => {
    const chunk: ChunkPreview = {
      id: "w",
      title: "Iris x Yasen",
      source: "telegram",
      source_id: "3938898465:1073-1080",
    };
    // Powers the "Open in chat" button — equals the chat id the backend
    // stores as Document.ConversationID.
    expect(chunkPreviewToHit(chunk).conversation_id).toBe("3938898465");
  });

  it("leaves conversation_id undefined for non-telegram sources", () => {
    const chunk: ChunkPreview = {
      id: "p",
      title: "Invoice",
      source: "paperless",
      source_id: "42",
    };
    expect(chunkPreviewToHit(chunk).conversation_id).toBeUndefined();
  });
});

describe("cardOwnsSnippet", () => {
  it("is true for sources whose body renders its own snippet", () => {
    for (const s of ["imap", "paperless", "filesystem", "ical"]) {
      expect(cardOwnsSnippet(s)).toBe(true);
    }
  });
  it("is false for telegram and unknown sources", () => {
    expect(cardOwnsSnippet("telegram")).toBe(false);
    expect(cardOwnsSnippet("whatever")).toBe(false);
  });
});
