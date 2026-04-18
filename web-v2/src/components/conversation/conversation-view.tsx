import { useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { ConversationHeader } from "./conversation-header";
import { MessageList } from "./message-list";
import { EdgeRail } from "./edge-rail";
import type { MessageRowModel } from "./message-row";

interface Props {
  sourceType: string;
  chatName?: string;
  participantCount?: number;
  rows: MessageRowModel[];
  isLoadingInitial: boolean;
  isFetchingOlder: boolean;
  isFetchingNewer: boolean;
  hasOlder: boolean;
  hasNewer: boolean;
  anchorSourceId?: string | null;
  onOlderIntersect?: () => void;
  onNewerIntersect?: () => void;
  onBack?: () => void;
}

export function ConversationView({
  sourceType,
  chatName,
  participantCount,
  rows,
  isLoadingInitial,
  isFetchingOlder,
  isFetchingNewer,
  hasOlder,
  hasNewer,
  anchorSourceId,
  onOlderIntersect,
  onNewerIntersect,
  onBack,
}: Props) {
  const scrollerRef = useRef<HTMLDivElement>(null);
  const olderRef = useRef<HTMLDivElement>(null);
  const newerRef = useRef<HTMLDivElement>(null);
  const hasPositionedRef = useRef(false);
  // Observers only activate after the initial scroll-to-anchor is
  // done. Otherwise both sentinels can appear in-viewport during
  // mount (especially in flex containers where min-height: auto
  // briefly lets the scroller expand to content), firing pagination
  // in a loop that never settles. Belt-and-braces against layout
  // regressions — even if the parent's height collapses, observers
  // never activate and we fail loudly (no scroll) rather than
  // silently loop.
  const [observersReady, setObserversReady] = useState(false);

  useEffect(() => {
    if (!observersReady || !onOlderIntersect || !hasOlder) return;
    const el = olderRef.current;
    const root = scrollerRef.current;
    if (!el || !root) return;
    const obs = new IntersectionObserver(
      (entries) => {
        if (
          entries.some((e) => e.isIntersecting) &&
          !isFetchingOlder &&
          hasOlder
        ) {
          onOlderIntersect();
        }
      },
      { root, rootMargin: "240px 0px 0px 0px" },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [observersReady, hasOlder, isFetchingOlder, onOlderIntersect]);

  useEffect(() => {
    if (!observersReady || !onNewerIntersect || !hasNewer) return;
    const el = newerRef.current;
    const root = scrollerRef.current;
    if (!el || !root) return;
    const obs = new IntersectionObserver(
      (entries) => {
        if (
          entries.some((e) => e.isIntersecting) &&
          !isFetchingNewer &&
          hasNewer
        ) {
          onNewerIntersect();
        }
      },
      { root, rootMargin: "0px 0px 240px 0px" },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [observersReady, hasNewer, isFetchingNewer, onNewerIntersect]);

  useEffect(() => {
    if (hasPositionedRef.current) return;
    if (isLoadingInitial || rows.length === 0) return;

    const scroller = scrollerRef.current;
    if (!scroller) return;

    if (anchorSourceId) {
      const el = scroller.querySelector<HTMLElement>(
        `#msg-${cssSafeId(anchorSourceId)}`,
      );
      if (el) {
        el.scrollIntoView({ block: "center" });
        hasPositionedRef.current = true;
      }
    }
    if (!hasPositionedRef.current) {
      scroller.scrollTop = scroller.scrollHeight;
      hasPositionedRef.current = true;
    }

    // Activate observers on the next frame so the browser has a
    // chance to compute the post-scroll layout before the initial
    // intersection callback fires. Empirically, firing in the same
    // frame as scrollIntoView leads to the old scroll state being
    // used by the observer.
    const handle = requestAnimationFrame(() => setObserversReady(true));
    return () => cancelAnimationFrame(handle);
  }, [anchorSourceId, isLoadingInitial, rows.length]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ConversationHeader
        sourceType={sourceType}
        chatName={chatName}
        participantCount={participantCount}
        onBack={onBack}
      />

      <div
        ref={scrollerRef}
        data-conversation-scroll
        className="relative min-h-0 flex-1 overflow-y-auto overscroll-contain"
      >
        <div className="mx-auto flex w-full max-w-3xl flex-col px-4 pb-8">
          {isLoadingInitial ? (
            <MessageSkeletons />
          ) : (
            <>
              {!hasOlder && rows.length > 0 && <EdgeRail />}

              {hasOlder && (
                <div
                  ref={olderRef}
                  className="flex items-center justify-center py-3"
                >
                  {isFetchingOlder && (
                    <span className="inline-flex items-center gap-2 rounded-full border border-border/60 bg-background px-3 py-1 text-[11.5px] text-muted-foreground">
                      <Loader2 className="size-3.5 animate-spin" aria-hidden />
                      Loading older…
                    </span>
                  )}
                </div>
              )}

              <MessageList rows={rows} />

              {hasNewer && (
                <div
                  ref={newerRef}
                  className="flex items-center justify-center py-3"
                >
                  {isFetchingNewer && (
                    <span className="inline-flex items-center gap-2 rounded-full border border-border/60 bg-background px-3 py-1 text-[11.5px] text-muted-foreground">
                      <Loader2 className="size-3.5 animate-spin" aria-hidden />
                      Loading newer…
                    </span>
                  )}
                </div>
              )}

              {rows.length === 0 && <EmptyState />}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function MessageSkeletons() {
  const widths = [62, 78, 48, 70, 56];
  return (
    <div className="flex flex-col gap-5 py-8">
      {widths.map((w, i) => (
        <div key={i} className="flex gap-3">
          <div className="size-8 shrink-0 animate-pulse rounded-full bg-muted/60" />
          <div className="flex-1 space-y-2">
            <div className="h-3.5 w-28 animate-pulse rounded bg-muted/60" />
            <div
              className="h-12 animate-pulse rounded-md bg-muted/35"
              style={{ width: `${w}%` }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
      <div className="text-[14px] font-medium text-foreground">
        No messages in this conversation.
      </div>
      <p className="max-w-sm text-[13px] text-muted-foreground">
        Either this chat is empty or the indexed window doesn&apos;t include any
        messages yet.
      </p>
    </div>
  );
}

function cssSafeId(id: string): string {
  return id.replace(/([^\w-])/g, "\\$1");
}
