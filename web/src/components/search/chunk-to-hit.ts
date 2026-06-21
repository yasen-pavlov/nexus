import type { ChunkPreview, DocumentHit } from "@/lib/api-types";

/**
 * Adapts a RAG ChunkPreview into a DocumentHit so the Ask evidence card can
 * render the SAME per-source card bodies as search (see SourceCardBody).
 *
 * Notes:
 *  - `source` is the source TYPE → maps to `source_type` (the card switch key).
 *  - `content` is left empty so the bodies' SnippetBody falls back to the
 *    `headline` only — the preview never ships raw content.
 *  - `relations` is `[]`, so isAttachmentHit() is false: imap chunks render
 *    the email body (attachment-vs-email routing needs a relations array,
 *    which previews don't carry — a deliberate follow-up).
 *  - `conversation_id` is derived for Telegram from the SourceID
 *    (`chatID:msgRange`), since previews don't carry the field explicitly.
 *    This is what powers the Telegram card's "Open in chat" affordance; it
 *    equals `Document.ConversationID` (the chat id) the backend would emit.
 *    Other sources leave it undefined — only Telegram opens the chat browser.
 */
export function chunkPreviewToHit(c: ChunkPreview): DocumentHit {
  return {
    id: c.id,
    source_type: c.source,
    source_name: c.source_name ?? "",
    source_id: c.source_id ?? "",
    title: c.title,
    content: "",
    mime_type: c.mime_type,
    size: c.size,
    metadata: c.metadata,
    url: c.url,
    visibility: "",
    created_at: c.date ?? "",
    indexed_at: "",
    headline: c.headline,
    rank: 0,
    related_count: 0,
    relations: [],
    conversation_id: telegramConversationID(c),
  };
}

// telegramConversationID extracts the chat id from a Telegram window's
// SourceID ("chatID:firstMsgID-lastMsgID"). Returns undefined for other
// sources or a malformed/empty SourceID.
function telegramConversationID(c: ChunkPreview): string | undefined {
  if (c.source !== "telegram") return undefined;
  const chatID = (c.source_id ?? "").split(":")[0];
  return chatID || undefined;
}
