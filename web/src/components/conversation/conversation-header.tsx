import { ArrowLeft } from "lucide-react";
import { SourceChip } from "@/components/source-chip";
import { Button } from "@/components/ui/button";

interface Props {
  sourceType: string;
  chatName?: string;
  participantCount?: number;
  onBack?: () => void;
}

export function ConversationHeader({
  sourceType,
  chatName,
  participantCount,
  onBack,
}: Props) {
  return (
    <header className="sticky top-0 z-20 flex items-center gap-3 border-b border-border/60 bg-background/85 px-4 py-3 backdrop-blur-sm">
      <Button
        variant="ghost"
        size="sm"
        onClick={onBack}
        aria-label="Back to search"
        className="size-8 shrink-0 rounded-md p-0 text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="size-4" aria-hidden />
      </Button>

      <div className="flex min-w-0 flex-1 items-center gap-2.5">
        <SourceChip type={sourceType} />
        <h1 className="min-w-0 truncate text-[15px] font-medium tracking-[-0.005em]">
          {chatName || "Conversation"}
        </h1>
        {participantCount !== undefined && participantCount > 1 && (
          <span className="shrink-0 text-[12px] tabular-nums text-muted-foreground/80">
            · {participantCount} participants
          </span>
        )}
      </div>
    </header>
  );
}
