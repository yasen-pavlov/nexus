import type { ChatMessage } from "@/lib/api-types";
import { formatRelative } from "@/lib/format";

export interface UserTurnProps {
  message: ChatMessage;
}

/**
 * Compact, right-aligned user turn. The asymmetric tr-md corner says
 * "from you" without the loud bubble of stock chat UIs.
 */
export function UserTurn({ message }: Readonly<UserTurnProps>) {
  return (
    <div className="ml-auto flex max-w-[80%] flex-col items-end gap-1">
      <div className="text-[11px] text-muted-foreground/80">
        {formatRelative(message.created_at)}
      </div>
      <div className="rounded-2xl rounded-tr-md border border-primary/20 bg-primary/8 px-4 py-2.5 text-[14.5px] leading-[22px] text-foreground">
        <p className="whitespace-pre-wrap break-words">{message.content}</p>
      </div>
    </div>
  );
}
