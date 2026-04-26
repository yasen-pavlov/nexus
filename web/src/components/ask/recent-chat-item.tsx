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

  return (
    <article
      aria-label={`Chat: ${title}`}
      className={cn(
        "group/recent relative flex flex-col gap-2 rounded-lg border border-border bg-card p-4 transition-colors",
        "hover:bg-card-hover hover:border-accent-foreground/20",
      )}
    >
      <Link
        to="/ask/$chatId"
        params={{ chatId: chat.id }}
        className="flex flex-col gap-1.5 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 rounded-md"
      >
        <div
          className={cn(
            "line-clamp-2 text-[14px] font-medium leading-[20px] text-foreground",
            // Reserve room for the absolutely-positioned delete affordance
            // in the top-right corner so 2-line titles never run under it.
            // Wider on hover/confirm to accommodate the "Delete?" pill.
            "pr-9 group-hover/recent:pr-24",
          )}
        >
          {title}
        </div>
        <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <span>{formatRelative(chat.updated_at)}</span>
          <span aria-hidden>·</span>
          <span className="font-mono tabular-nums opacity-70">{shortID}</span>
        </div>
      </Link>

      <div className="absolute right-2 top-2">
        {confirm ? (
          <div className="flex items-center gap-1 rounded-md border border-destructive/30 bg-destructive/10 px-1.5 py-0.5 text-[11px] text-destructive">
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
                e.preventDefault();
                e.stopPropagation();
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
              e.preventDefault();
              e.stopPropagation();
              setConfirm(true);
            }}
            aria-label="Delete chat"
            className={cn(
              "inline-flex size-7 items-center justify-center rounded-md text-muted-foreground transition-opacity",
              "hover:bg-destructive/10 hover:text-destructive",
              "opacity-0 group-hover/recent:opacity-100 focus-visible:opacity-100",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
            )}
          >
            <Trash2 className="size-3.5" aria-hidden />
          </button>
        )}
      </div>
    </article>
  );
}
