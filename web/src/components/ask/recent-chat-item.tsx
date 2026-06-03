import { Link } from "@tanstack/react-router";
import { Check, Pencil, Trash2, X } from "lucide-react";
import { useState } from "react";

import type { ChatListEntry } from "@/lib/api-types";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

export interface RecentChatItemProps {
  chat: ChatListEntry;
  onDelete: (id: string) => Promise<void>;
  onRename: (id: string, title: string) => Promise<void>;
}

const SHORT_ID_LEN = 8;
const MAX_TITLE_LEN = 120;

/**
 * One row in the recent-chats grid on /ask. Title resolves with the
 * Phase-3 fallback chain: explicit chat.title (auto-titled or
 * user-renamed) → first_message_preview → "Untitled chat".
 *
 * Two footer affordances — rename (pencil) and delete (trash) — share
 * the metadata row so there's no layout shift on hover. Both expand into
 * an inline editor/confirm in the SAME slot: rename swaps the title for
 * an input (Enter saves, Esc cancels); delete swaps the footer for a
 * confirm pill. Only one mode is active at a time.
 */
export function RecentChatItem({
  chat,
  onDelete,
  onRename,
}: Readonly<RecentChatItemProps>) {
  const [confirm, setConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);

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

  const startEdit = (e: React.MouseEvent) => {
    stop(e);
    // Seed with the persisted title only (not the message-preview
    // fallback) — renaming should start from the real title, blank if
    // none, so the user isn't editing a preview they didn't write.
    setDraft(chat.title?.trim() ?? "");
    setEditing(true);
  };

  const commitEdit = async () => {
    const next = draft.trim();
    if (!next || next === chat.title?.trim()) {
      setEditing(false);
      return;
    }
    setSaving(true);
    try {
      await onRename(chat.id, next);
      setEditing(false);
    } finally {
      setSaving(false);
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
      {editing ? (
        <div className="flex min-h-[40px] flex-1 items-start">
          <input
            type="text"
            autoFocus
            value={draft}
            maxLength={MAX_TITLE_LEN}
            disabled={saving}
            onChange={(e) => setDraft(e.target.value)}
            onClick={stop}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                void commitEdit();
              } else if (e.key === "Escape") {
                e.preventDefault();
                setEditing(false);
              }
            }}
            aria-label="Chat title"
            className={cn(
              "w-full rounded-md border border-border bg-background px-2 py-1 text-[14px] font-medium leading-[20px] text-foreground outline-none",
              "focus-visible:ring-2 focus-visible:ring-ring/40 disabled:opacity-50",
            )}
          />
        </div>
      ) : (
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
      )}

      <div className="flex h-7 items-center gap-1.5 text-[11px] text-muted-foreground">
        <span>{formatRelative(chat.updated_at)}</span>
        <span aria-hidden>·</span>
        <span className="font-mono tabular-nums opacity-70">{shortID}</span>
        <div className="ml-auto flex items-center gap-0.5" onClick={stop}>
          {editing ? (
            <div className="flex items-center gap-1 rounded-md border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-primary">
              <span className="font-medium">Rename</span>
              <button
                type="button"
                onClick={() => void commitEdit()}
                disabled={saving}
                aria-label="Save title"
                className="inline-flex size-5 items-center justify-center rounded hover:bg-primary/20 disabled:opacity-50"
              >
                <Check className="size-3" aria-hidden />
              </button>
              <button
                type="button"
                onClick={(e) => {
                  stop(e);
                  setEditing(false);
                }}
                aria-label="Cancel rename"
                className="inline-flex size-5 items-center justify-center rounded hover:bg-primary/20"
              >
                <X className="size-3" aria-hidden />
              </button>
            </div>
          ) : confirm ? (
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
            <>
              <button
                type="button"
                onClick={startEdit}
                aria-label="Rename chat"
                className={cn(
                  "inline-flex size-6 items-center justify-center rounded text-muted-foreground/50 transition-colors",
                  "hover:bg-primary/10 hover:text-primary",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                )}
              >
                <Pencil className="size-3.5" aria-hidden />
              </button>
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
            </>
          )}
        </div>
      </div>
    </article>
  );
}
