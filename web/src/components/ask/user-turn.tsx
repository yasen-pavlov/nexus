import type { ChatMessage } from "@/lib/api-types";
import { formatRelative } from "@/lib/format";

export interface UserTurnProps {
  message: ChatMessage;
  /** The immediately-following persisted message in the thread (the
   *  assistant turn that answered this user message). Used to surface
   *  the rewriter's normalised query as a retroactive footnote, so a
   *  user who reloads an old chat can still see what the system
   *  actually searched for on this follow-up. */
  nextMessage?: ChatMessage;
}

/**
 * Compact, right-aligned user turn. The asymmetric tr-md corner says
 * "from you" without the loud bubble of stock chat UIs.
 *
 * When the follow-up assistant turn carried a `rewritten_query` (the
 * rewriter resolved coreference into a self-contained search query),
 * we surface the rewritten phrase as a small italic footnote under
 * the bubble. Helps post-hoc debug: "why did this turn retrieve X?".
 */
export function UserTurn({ message, nextMessage }: Readonly<UserTurnProps>) {
  const rewritten =
    nextMessage?.role === "assistant" &&
    nextMessage.rewritten_query &&
    nextMessage.rewritten_query !== message.content
      ? nextMessage.rewritten_query
      : "";

  return (
    <div className="ml-auto flex max-w-[80%] flex-col items-end gap-1">
      <div className="text-[11px] text-muted-foreground/80">
        {formatRelative(message.created_at)}
      </div>
      <div className="rounded-2xl rounded-tr-md border border-primary/20 bg-primary/8 px-4 py-2.5 text-[14.5px] leading-[22px] text-foreground">
        <p className="whitespace-pre-wrap break-words">{message.content}</p>
      </div>
      {rewritten && (
        <div className="max-w-full text-[11px] text-muted-foreground/80">
          <span className="text-muted-foreground/60">searched as</span>{" "}
          <span className="italic text-muted-foreground">{rewritten}</span>
        </div>
      )}
    </div>
  );
}
