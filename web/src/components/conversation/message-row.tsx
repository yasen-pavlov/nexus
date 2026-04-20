import { memo } from "react";
import { format } from "date-fns";
import { cn } from "@/lib/utils";
import { useAvatarBlob } from "@/hooks/use-avatar-blob";
import { useDocumentBySource } from "@/hooks/use-document-by-source";
import { SenderAvatar } from "./sender-avatar";
import { ReplyQuote, type ReplyQuoteState } from "./reply-quote";
import { AttachmentChip, AttachmentChipRow } from "./attachment-chip";
import { InlineImage, InlineVideo } from "./inline-media";
import { mimeIsImage, mimeIsVideo } from "./mime-helpers";
import { AnchorRing } from "./anchor-ring";

export type GroupPosition = "solo" | "first" | "mid" | "last";

export interface AttachmentModel {
  id: string;
  filename: string;
  size?: number;
  mimeType?: string;
  onDownload: () => void;
}

export interface PendingReplyTarget {
  sourceType: string;
  sourceId: string;
}

export interface MessageRowModel {
  sourceId: string;
  senderId: string | null;
  senderName: string | null;
  senderUsername?: string | null;
  createdAt: string;
  body: string;
  // In-range reply quote (resolved synchronously from the loaded window).
  replyQuote?: ReplyQuoteState;
  // Out-of-range reply pointer — when present, MessageRow fires a
  // lazy /documents/by-source fetch and renders the resolved state.
  pendingReplyTarget?: PendingReplyTarget;
  attachments?: AttachmentModel[];
  // avatarKey + connectorId pair drives authed avatar fetch; when
  // absent or unresolved the row falls back to deterministic initials.
  avatarKey?: string | null;
  connectorId?: string | null;
  isSelf: boolean;
  isAnchor: boolean;
  position: GroupPosition;
}

// ConnectedSenderAvatar wraps SenderAvatar with the authed blob fetch.
// Kept inline to this component because avatar connection is strictly
// a message-row concern — other SenderAvatar consumers (e.g. tests)
// supply blobUrl directly.
const ConnectedSenderAvatar = memo(function ConnectedSenderAvatar({
  connectorId,
  externalId,
  senderName,
  seed,
}: {
  connectorId: string;
  externalId: string;
  senderName: string | null;
  seed: string;
}) {
  const { data } = useAvatarBlob(connectorId, externalId);
  return (
    <SenderAvatar
      blobUrl={data ?? null}
      senderName={senderName}
      seed={seed}
    />
  );
});

// LazyReplyQuote fetches the reply target lazily when the quoted
// message isn't loaded in the current window. Returns the resolved
// state to ReplyQuote; the pure ReplyQuote handles visual states.
function LazyReplyQuote({ target }: { target: PendingReplyTarget }) {
  const { data, isLoading, isError } = useDocumentBySource(
    target.sourceType,
    target.sourceId,
  );

  if (isLoading) {
    return <ReplyQuote state={{ status: "loading" }} />;
  }
  if (isError || !data) {
    return <ReplyQuote state={{ status: "unavailable" }} />;
  }

  const authorName = readSenderName(data) ?? "Someone";
  const snippet = truncate(data.content ?? "(no text)", 180);
  return (
    <ReplyQuote
      state={{
        status: "loaded",
        authorName,
        snippet,
        inRange: false,
      }}
    />
  );
}

function readSenderName(d: {
  metadata?: Record<string, unknown>;
}): string | null {
  const n = d.metadata?.sender_name;
  return typeof n === "string" && n.trim() !== "" ? n : null;
}

// AttachmentRow partitions attachments into inline media (images,
// videos) and file chips. Inline media renders above the chip row so
// photos/videos stack vertically at usable size; the chip row shows
// any remaining files (PDFs, documents, etc.) click-to-download.
// Renders inside the message bubble so media sits flush with the
// caption/body text instead of trailing in a separate container.
function AttachmentRow({ attachments }: { attachments: AttachmentModel[] }) {
  const inline: AttachmentModel[] = [];
  const files: AttachmentModel[] = [];
  for (const att of attachments) {
    if (mimeIsImage(att.mimeType) || mimeIsVideo(att.mimeType)) {
      inline.push(att);
    } else {
      files.push(att);
    }
  }

  return (
    <>
      {inline.length > 0 && (
        <div className="flex flex-col gap-2">
          {inline.map((a) =>
            mimeIsImage(a.mimeType) ? (
              <InlineImage key={a.id} id={a.id} filename={a.filename} />
            ) : (
              <InlineVideo key={a.id} id={a.id} filename={a.filename} />
            ),
          )}
        </div>
      )}
      {files.length > 0 && (
        <AttachmentChipRow>
          {files.map((a) => (
            <AttachmentChip
              key={a.id}
              filename={a.filename}
              size={a.size}
              onDownload={a.onDownload}
            />
          ))}
        </AttachmentChipRow>
      )}
    </>
  );
}

// groupedBubbleRadius returns the corner-rounding classes a message
// bubble wears based on its position in a same-sender burst. Shared
// edges are fully squared (rounded-none) so consecutive bubbles sit
// flush and read as one connected block — outer edges keep the
// normal rounded-md treatment. Combined with zero inter-row padding
// on tight positions (see the article classes above), a burst of
// messages renders as a single continuous panel with visual
// breathing room between bubbles coming purely from their own
// internal padding.
function groupedBubbleRadius(position: GroupPosition): string {
  switch (position) {
    case "solo":
      return "rounded-md";
    case "first":
      return "rounded-t-md rounded-b-none";
    case "mid":
      return "rounded-none";
    case "last":
      return "rounded-b-md rounded-t-none";
  }
}

function truncate(s: string, n: number): string {
  const one = s.replaceAll(/\s+/g, " ").trim();
  if (one.length <= n) return one;
  return one.slice(0, n - 1).trimEnd() + "…";
}

interface Props {
  model: MessageRowModel;
}

export const MessageRow = memo(function MessageRow({ model }: Props) {
  const showHeader = model.position === "first" || model.position === "solo";
  const tightTop = model.position === "mid" || model.position === "last";
  const tightBottom = model.position === "first" || model.position === "mid";

  const row = (
    <article
      id={`msg-${model.sourceId}`}
      tabIndex={0}
      data-group={model.position}
      className={cn(
        "group flex gap-3 px-2 outline-none",
        // Grouped rows have zero inter-row padding so the bubbles sit
        // flush against each other — combined with squared shared
        // corners (see groupedBubbleRadius) they read as one block.
        tightTop ? "pt-0" : "pt-3",
        tightBottom ? "pb-0" : "pb-2",
        // Focus ring only; hover feedback moves down to the bubble so
        // it tracks the bubble's actual width/shape instead of
        // overflowing across the avatar gutter and row padding.
        "focus-visible:rounded-md focus-visible:ring-2 focus-visible:ring-primary/40",
      )}
    >
      <div className="w-8 shrink-0 pt-0.5">
        {showHeader ? (
          model.avatarKey && model.connectorId && model.senderId ? (
            <ConnectedSenderAvatar
              connectorId={model.connectorId}
              externalId={model.senderId}
              senderName={model.senderName}
              seed={model.senderId || model.senderName || "anon"}
            />
          ) : (
            <SenderAvatar
              senderName={model.senderName}
              seed={model.senderId || model.senderName || "anon"}
            />
          )
        ) : (
          <div className="size-8" aria-hidden />
        )}
      </div>

      <div className="min-w-0 flex-1">
        {showHeader && (
          <div className="mb-0.5 flex items-baseline gap-1.5">
            <span
              className={cn(
                "truncate text-[14px] font-medium tracking-[-0.005em]",
                model.isSelf ? "text-primary" : "text-foreground",
              )}
            >
              {model.senderName || "Unknown"}
            </span>
            {model.senderUsername && (
              <span className="min-w-0 truncate text-[12.5px] text-muted-foreground/85">
                @{model.senderUsername}
              </span>
            )}
            <span className="ml-auto shrink-0 pl-2 text-[11.5px] tabular-nums text-muted-foreground/80">
              {format(new Date(model.createdAt), "h:mm a")}
            </span>
          </div>
        )}

        {model.replyQuote && <ReplyQuote state={model.replyQuote} />}
        {!model.replyQuote && model.pendingReplyTarget && (
          <LazyReplyQuote target={model.pendingReplyTarget} />
        )}

        <div
          className={cn(
            // Shared bubble shape + text treatment. Corners are
            // squared on edges that abut another bubble in the same
            // sender burst so consecutive messages read as one
            // connected block (iMessage / Linear pattern).
            "flex flex-col gap-2 px-3 py-2 text-[14px] leading-[21px] break-words whitespace-pre-wrap transition-colors",
            groupedBubbleRadius(model.position),
            model.isSelf
              ? "bg-primary/[0.07] text-foreground group-hover:bg-primary/[0.11]"
              : "bg-muted/30 text-foreground/95 group-hover:bg-muted/55",
            !model.body &&
              (!model.attachments || model.attachments.length === 0) &&
              "italic text-muted-foreground/80",
          )}
        >
          {model.body ? (
            <span>{model.body}</span>
          ) : !model.attachments || model.attachments.length === 0 ? (
            <span>(no text)</span>
          ) : null}

          {model.attachments && model.attachments.length > 0 && (
            <AttachmentRow attachments={model.attachments} />
          )}
        </div>
      </div>
    </article>
  );

  if (model.isAnchor) {
    return <AnchorRing active>{row}</AnchorRing>;
  }
  return row;
});
