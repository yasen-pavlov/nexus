import { useMemo } from "react";
import type { Document } from "@/lib/api-types";
import { useConversation } from "@/hooks/use-conversation";
import { useIdentities } from "@/hooks/use-identities";
import { useDocumentDownload } from "@/hooks/use-document-download";
import { ConversationView } from "./conversation-view";
import type {
  AttachmentModel,
  MessageRowModel,
  PendingReplyTarget,
} from "./message-row";
import type { ReplyQuoteState } from "./reply-quote";

interface Props {
  sourceType: string;
  conversationId: string;
  anchorId?: number;
  anchorTs?: string;
  onBack?: () => void;
}

// ConversationPage wires the conversation view to real data: conversation
// messages, self-identity, avatar connector wiring, reply quote
// resolution (in-range vs lazy fetch), and attachment downloads.
//
// It's the only component in the conversation/ directory that calls
// hooks against the API — every other component is presentational so
// MSW tests and design iteration stay fast.
export function ConversationPage({
  sourceType,
  conversationId,
  anchorId,
  anchorTs,
  onBack,
}: Readonly<Props>) {
  const {
    messages,
    isLoadingInitial,
    isFetchingOlder,
    isFetchingNewer,
    hasOlder,
    hasNewer,
    fetchOlder,
    fetchNewer,
  } = useConversation(sourceType, conversationId, { anchorTs });

  const { bySourceType } = useIdentities();
  const selfIdentity = bySourceType.get(sourceType);
  const download = useDocumentDownload();

  // In-range lookup: map of source_id → Document. Reply quotes check
  // this first; misses fall through to lazy fetch. Cheap O(n) build
  // once per messages change.
  const bySourceID = useMemo(() => {
    const m = new Map<string, Document>();
    for (const msg of messages) m.set(msg.source_id, msg);
    return m;
  }, [messages]);

  // The anchor source_id needs to be derived from the route param
  // (anchor_id is the Telegram message_id) plus the conversation
  // identifier — the per-message source_id format is well-known.
  // See internal/connector/telegram/connector.go makeMessageDoc.
  const anchorSourceId = useMemo(() => {
    if (sourceType !== "telegram" || anchorId === undefined) return null;
    return `${conversationId}:${anchorId}:msg`;
  }, [sourceType, conversationId, anchorId]);

  const chatName = useMemo<string | undefined>(() => {
    for (const m of messages) {
      const n = m.metadata?.chat_name;
      if (typeof n === "string" && n.trim() !== "") return n;
    }
    return undefined;
  }, [messages]);

  const rows = useMemo<MessageRowModel[]>(() => {
    const ownerExternalID = selfIdentity?.external_id ?? null;
    const connectorID = selfIdentity?.connector_id ?? null;

    // Drop messages with neither body text nor attachments — these are
    // usually stickers or other unsupported Telegram media (polls,
    // geo, venues) where the media got rejected upstream but the
    // per-message doc was still emitted. Rendering them as "(no text)"
    // bubbles adds noise without context; quietly skipping reads as
    // the natural chat flow.
    const visible = messages.filter((m) => {
      if (m.content && m.content.trim() !== "") return true;
      const atts = m.metadata?.attachments;
      return Array.isArray(atts) && atts.length > 0;
    });

    return visible.map((m) =>
      messageToRowModel(m, {
        sourceType,
        anchorSourceId,
        ownerExternalID,
        connectorID,
        bySourceID,
        onDownload: (id: string, filename: string) =>
          download.mutate({ id, suggestedFilename: filename }),
      }),
    );
  }, [
    messages,
    sourceType,
    anchorSourceId,
    selfIdentity?.external_id,
    selfIdentity?.connector_id,
    bySourceID,
    download,
  ]);

  return (
    <ConversationView
      sourceType={sourceType}
      chatName={chatName}
      rows={rows}
      isLoadingInitial={isLoadingInitial}
      isFetchingOlder={isFetchingOlder}
      isFetchingNewer={isFetchingNewer}
      hasOlder={hasOlder}
      hasNewer={hasNewer}
      anchorSourceId={anchorSourceId}
      onOlderIntersect={() => {
        void fetchOlder();
      }}
      onNewerIntersect={() => {
        void fetchNewer();
      }}
      onBack={onBack}
    />
  );
}

interface MapperContext {
  sourceType: string;
  anchorSourceId: string | null;
  ownerExternalID: string | null;
  connectorID: string | null;
  bySourceID: Map<string, Document>;
  onDownload: (id: string, filename: string) => void;
}

function messageToRowModel(m: Document, ctx: MapperContext): MessageRowModel {
  const senderId = readSenderId(m);
  const senderName = readString(m.metadata?.sender_name) ?? null;
  const senderUsername = readString(m.metadata?.sender_username);
  const avatarKey = readString(m.metadata?.sender_avatar_key) ?? null;

  const isSelf =
    ctx.ownerExternalID !== null &&
    senderId !== null &&
    ctx.ownerExternalID === senderId;

  const reply = buildReplyResolution(m, ctx);
  const attachments = buildAttachments(m, ctx);

  return {
    sourceId: m.source_id,
    senderId,
    senderName,
    senderUsername,
    createdAt: m.created_at,
    body: m.content ?? "",
    replyQuote: reply.resolved,
    pendingReplyTarget: reply.pending,
    attachments,
    avatarKey,
    connectorId: ctx.connectorID,
    isSelf,
    isAnchor: ctx.anchorSourceId !== null && ctx.anchorSourceId === m.source_id,
    // Position is assigned inside MessageList's reducer based on
    // neighbor burst detection — this value is a placeholder that the
    // list overwrites.
    position: "solo",
  };
}

interface ReplyResolution {
  resolved?: ReplyQuoteState;
  pending?: PendingReplyTarget;
}

function buildReplyResolution(m: Document, ctx: MapperContext): ReplyResolution {
  const rel = (m.relations ?? []).find((r) => r.type === "reply_to");
  if (!rel?.target_source_id) return {};

  const target = ctx.bySourceID.get(rel.target_source_id);
  if (target) {
    return {
      resolved: {
        status: "loaded",
        authorName: senderDisplayName(target) ?? "Someone",
        snippet: truncate(target.content ?? "(no text)", 180),
        inRange: true,
        onJump: () => scrollToSourceID(rel.target_source_id!),
      },
    };
  }

  // Out-of-range — MessageRow renders <LazyReplyQuote> to fetch it.
  return {
    pending: {
      sourceType: ctx.sourceType,
      sourceId: rel.target_source_id,
    },
  };
}

interface AttachmentMetaEntry {
  id?: unknown;
  filename?: unknown;
  size?: unknown;
  mime_type?: unknown;
}

function buildAttachments(
  m: Document,
  ctx: MapperContext,
): AttachmentModel[] | undefined {
  const raw = m.metadata?.attachments;
  if (!Array.isArray(raw) || raw.length === 0) return undefined;

  const out: AttachmentModel[] = [];
  for (const entry of raw as AttachmentMetaEntry[]) {
    const id = readString(entry?.id);
    const filename = readString(entry?.filename) ?? "attachment";
    if (!id) continue;
    const size = typeof entry?.size === "number" ? entry.size : undefined;
    const mimeType = readString(entry?.mime_type);
    out.push({
      id,
      filename,
      size,
      mimeType,
      onDownload: () => ctx.onDownload(id, filename),
    });
  }
  return out.length > 0 ? out : undefined;
}

function readSenderId(m: Document): string | null {
  const raw = m.metadata?.sender_id;
  if (typeof raw === "number") return String(raw);
  if (typeof raw === "string" && raw.trim() !== "") return raw;
  return null;
}

function readString(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() !== "" ? v : undefined;
}

function senderDisplayName(m: Document): string | null {
  const n = readString(m.metadata?.sender_name);
  return n ?? null;
}

function truncate(s: string, n: number): string {
  const one = s.replaceAll(/\s+/g, " ").trim();
  if (one.length <= n) return one;
  return one.slice(0, n - 1).trimEnd() + "…";
}

function scrollToSourceID(sourceID: string) {
  const id = sourceID.replaceAll(/[^\w-]/g, String.raw`\$&`);
  const el = document.querySelector<HTMLElement>(`#msg-${id}`);
  if (!el) return;
  el.scrollIntoView({ block: "center", behavior: "smooth" });
  el.focus({ preventScroll: true });
}
