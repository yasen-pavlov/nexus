import type { DocumentHit } from "@/lib/api-types";
import { EmailCardBody } from "./cards/email";
import { AttachmentCardBody } from "./cards/attachment";
import { TelegramCardBody } from "./cards/telegram";
import { PaperlessCardBody } from "./cards/paperless";
import { FilesystemCardBody } from "./cards/filesystem";
import { CalendarCardBody } from "./cards/calendar";
import { DefaultCardBody } from "./cards/default";

function isAttachmentHit(hit: DocumentHit): boolean {
  return !!hit.relations?.some((r) => r.type === "attachment_of");
}

const noopOpenChat = () => {};

interface SourceCardBodyProps {
  hit: DocumentHit;
  /** Telegram "open in chat" (only fires when the hit carries a conversation_id). */
  onOpenChat?: (hit: DocumentHit) => void;
  /** Download/open the document (paperless, filesystem, attachment). */
  onDownload?: (hit: DocumentHit) => void;
  /** Download a named email attachment. */
  onAttachmentDownload?: (att: { id: string; filename: string }) => void;
}

/**
 * Routes a DocumentHit to its source-specific content region — the single
 * source of truth for the rich per-source layouts. Used both by the search
 * ResultCard and (via a ChunkPreview→DocumentHit adapter) by the Ask
 * evidence card, so both surfaces render identical cards.
 */
export function SourceCardBody({
  hit,
  onOpenChat,
  onDownload,
  onAttachmentDownload,
}: Readonly<SourceCardBodyProps>) {
  switch (hit.source_type) {
    case "imap":
      return isAttachmentHit(hit) ? (
        <AttachmentCardBody hit={hit} onDownload={onDownload} />
      ) : (
        <EmailCardBody hit={hit} onAttachmentClick={onAttachmentDownload} />
      );
    case "telegram":
      return <TelegramCardBody hit={hit} onOpenChat={onOpenChat ?? noopOpenChat} />;
    case "paperless":
      return <PaperlessCardBody hit={hit} onDownload={onDownload} />;
    case "filesystem":
      return <FilesystemCardBody hit={hit} onDownload={onDownload} />;
    case "ical":
      return <CalendarCardBody hit={hit} />;
    default:
      return <DefaultCardBody hit={hit} />;
  }
}
