import { Link } from "@tanstack/react-router";
import { Check, Trash2, X } from "lucide-react";
import { useState } from "react";

import type { ChatListEntry } from "@/lib/api-types";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

export interface RecentChatItemProps {
  chat: ChatListEntry;
  onDelete: (id: string) => Promise<void>;
}

const SHORT_ID_LEN = 8;

/**
 * One row in the recent-chats grid on /ask. Title resolves with the
 * Phase-3 fallback chain: explicit chat.title (Phase 4 will populate
 * via auto-titles) → first_message_preview → "Untitled chat".
 *
 * The delete affordance lives in a footer row alongside the metadata
 * — same render slot whether resting or in confirm mode, so there's
 * no layout shift on hover and the confirm pill never overlaps the
 * title text.
 */
export function RecentChatItem({
  chat,
  onDelete,
}: Readonly<RecentChatItemProps>) {
  const [confirm, setConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const title =
    chat.title?.trim() ||
    chat.first_message_preview?.trim() ||
    "Untitled chat";
  const shortID = chat.id.slice(0, SHORT_ID_LEN) + "…";

  const handleDelete = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDeleting(true);
    try {
      await onDelete(chat.id);
    } finally {
      setDeleting(false);
      setConfirm(false);
    }
  };

  const stop = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };

  return (
    <article
      aria-label={`Chat: ${title}`}
      className={cn(
        "relative flex h-full flex-col gap-3 rounded-lg border border-border bg-card p-4 transition-colors",
        "hover:bg-card-hover hover:border-accent-foreground/20",
        confirm && "border-destructive/30 bg-destructive/5 hover:border-destructive/30",
      )}
    >
      <Link
        to="/ask/$chatId"
        params={{ chatId: chat.id }}
        // flex-1 pushes the footer to the bottom; min-h reserves
        // space for two lines so single-line titles don't make the
        // card shorter than its two-line siblings in the grid row.
        className="flex flex-1 flex-col gap-1.5 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <div className="line-clamp-2 min-h-[40px] text-[14px] font-medium leading-[20px] text-foreground">
          {title}
        </div>
      </Link>

      <div className="flex h-7 items-center gap-1.5 text-[11px] text-muted-foreground">
        <span>{formatRelative(chat.updated_at)}</span>
        <span aria-hidden>·</span>
        <span className="font-mono tabular-nums opacity-70">{shortID}</span>
        <div className="ml-auto flex items-center" onClick={stop}>
          {confirm ? (
            <div className="flex items-center gap-1 rounded-md border border-destructive/30 bg-destructive/10 px-1.5 py-0.5 text-destructive">
              <span className="font-medium">Delete?</span>
              <button
                type="button"
                onClick={handleDelete}
                disabled={deleting}
                aria-label="Confirm delete"
                className="inline-flex size-5 items-center justify-center rounded hover:bg-destructive/20 disabled:opacity-50"
              >
                <Check className="size-3" aria-hidden />
              </button>
              <button
                type="button"
                onClick={(e) => {
                  stop(e);
                  setConfirm(false);
                }}
                aria-label="Cancel delete"
                className="inline-flex size-5 items-center justify-center rounded hover:bg-destructive/20"
              >
                <X className="size-3" aria-hidden />
              </button>
            </div>
          ) : (
            <button
              type="button"
              onClick={(e) => {
                stop(e);
                setConfirm(true);
              }}
              aria-label="Delete chat"
              className={cn(
                "inline-flex size-6 items-center justify-center rounded text-muted-foreground/50 transition-colors",
                "hover:bg-destructive/10 hover:text-destructive",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
              )}
            >
              <Trash2 className="size-3.5" aria-hidden />
            </button>
          )}
        </div>
      </div>
    </article>
  );
}
