import { CornerUpLeft } from "lucide-react";
import { cn } from "@/lib/utils";

export type ReplyQuoteState =
  | { status: "loading" }
  | { status: "unavailable" }
  | {
      status: "loaded";
      authorName: string;
      snippet: string;
      inRange: boolean;
      onJump?: () => void;
    };

interface Props {
  state: ReplyQuoteState;
}

export function ReplyQuote({ state }: Readonly<Props>) {
  if (state.status === "loading") {
    return (
      <div className="mb-1.5 flex items-center gap-2 rounded-r-md border-l-2 border-border/60 bg-muted/25 pl-2.5 pr-3 py-1 text-[12.5px]">
        <CornerUpLeft
          className="size-3.5 shrink-0 text-muted-foreground/60"
          aria-hidden
        />
        <span className="h-3 w-24 animate-pulse rounded bg-muted-foreground/20" />
        <span className="h-3 w-40 animate-pulse rounded bg-muted-foreground/15" />
      </div>
    );
  }

  if (state.status === "unavailable") {
    return (
      <div className="mb-1.5 flex items-center gap-1.5 rounded-r-md border-l-2 border-border/40 bg-muted/15 pl-2.5 pr-3 py-1 text-[12.5px] italic text-muted-foreground/70">
        <CornerUpLeft className="size-3.5 shrink-0" aria-hidden />
        <span>unavailable</span>
      </div>
    );
  }

  const clickable = state.inRange && !!state.onJump;
  const commonClasses =
    "mb-1.5 flex min-w-0 max-w-full items-center gap-1.5 rounded-r-md border-l-2 border-border/80 bg-muted/30 pl-2.5 pr-3 py-1 text-left text-[12.5px] transition-colors";

  if (clickable) {
    return (
      <button
        type="button"
        onClick={state.onJump}
        className={cn(
          commonClasses,
          "cursor-pointer hover:bg-muted/55 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40",
        )}
      >
        <ReplyQuoteInner
          authorName={state.authorName}
          snippet={state.snippet}
        />
      </button>
    );
  }

  return (
    <div
      title="earlier in this conversation"
      className={cn(commonClasses, "cursor-default")}
    >
      <ReplyQuoteInner
        authorName={state.authorName}
        snippet={state.snippet}
      />
    </div>
  );
}

function ReplyQuoteInner({
  authorName,
  snippet,
}: Readonly<{
  authorName: string;
  snippet: string;
}>) {
  return (
    <>
      <CornerUpLeft
        className="size-3.5 shrink-0 text-muted-foreground"
        aria-hidden
      />
      <span className="shrink-0 font-medium text-foreground/90">
        {authorName}
      </span>
      <span className="min-w-0 flex-1 truncate text-muted-foreground">
        {snippet}
      </span>
    </>
  );
}
