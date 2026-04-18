import { ArrowRight, MessageCircle } from "lucide-react";
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

  if (!chatName && messageCount === undefined && !canOpen) return null;

  return (
    <div className="mt-2 flex items-center justify-between gap-3 text-[13px]">
      <div className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
        <MessageCircle
          className="size-3.5 shrink-0 text-muted-foreground/70"
          aria-hidden
        />
        {chatName ? (
          <span className="truncate font-medium text-foreground/90">
            {chatName}
          </span>
        ) : (
          <span className="truncate">Telegram chat</span>
        )}
        {messageCount !== undefined && messageCount > 1 && (
          <>
            <span className="shrink-0 text-muted-foreground/50">·</span>
            <span className="shrink-0 tabular-nums">
              {messageCount} messages
            </span>
          </>
        )}
      </div>

      {canOpen && (
        <Button
          variant="outline"
          size="sm"
          onClick={() => onOpenChat?.(hit)}
          className="h-7 shrink-0 gap-1.5 rounded-md px-2.5 text-[12.5px] font-medium"
        >
          Open in chat
          <ArrowRight className="size-3.5" aria-hidden />
        </Button>
      )}
    </div>
  );
}
