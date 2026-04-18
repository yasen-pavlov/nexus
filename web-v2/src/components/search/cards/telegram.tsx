import { MessageCircle } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { Button } from "@/components/ui/button";

function str(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() ? v : undefined;
}

function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

interface Props {
  hit: DocumentHit;
  onOpenChat?: (hit: DocumentHit) => void;
}

export function TelegramCardBody({ hit, onOpenChat }: Props) {
  const m = hit.metadata ?? {};
  const chatName = str(m.chat_name);
  const messageCount = num(m.message_count);
  const canOpen = !!hit.conversation_id && !!onOpenChat;

  return (
    <div className="mt-2 flex items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-2 text-sm">
        {chatName ? (
          <>
            <span
              className="shrink-0 font-mono text-base leading-none text-muted-foreground/70"
              aria-hidden
            >
              #
            </span>
            <span className="truncate font-medium">{chatName}</span>
          </>
        ) : (
          <span className="truncate text-muted-foreground">Telegram chat</span>
        )}
        {messageCount !== undefined && messageCount > 1 && (
          <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
            · {messageCount} messages
          </span>
        )}
      </div>

      {canOpen && (
        <Button
          variant="secondary"
          size="sm"
          className="h-7 shrink-0"
          onClick={() => onOpenChat?.(hit)}
        >
          <MessageCircle className="mr-1 size-3.5" aria-hidden />
          Open in chat
        </Button>
      )}
    </div>
  );
}
