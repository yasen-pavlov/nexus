import { useAnimatedNumber } from "@/hooks/use-animated-number";
import { cn } from "@/lib/utils";

export interface SyncProgressProps {
  processed: number;
  total: number;
  errors?: number;
  /** Free-form "what's happening now" label from the connector
   *  (IMAP folder, Telegram chat, etc.). Rendered alongside the
   *  counter so "0/327" becomes "Archive · 0/327". */
  scope?: string;
  /** When true, emits a thin bar with no label row — used inside the card. */
  compact?: boolean;
  className?: string;
}

/**
 * Two-layer progress indicator: a neutral track + a marmalade fill. When
 * docs_total is 0 we show an indeterminate gliding bead (`ticker-bead`
 * keyframe), because the connector's Fetch hasn't reported a total yet —
 * the "discovering" phase shouldn't look broken.
 */
export function SyncProgress({
  processed,
  total,
  errors = 0,
  scope,
  compact,
  className,
}: Readonly<SyncProgressProps>) {
  // Smooth the counter — the SSE stream fires every per-doc event
  // the backend emits, but React batches state updates within a
  // microtask so a bursty connector (iCloud returning 100 envelopes
  // at once) would render as 20+ visible jumps. Tweening the
  // displayed numbers makes the text tick at a human-readable
  // cadence even when the underlying state hops.
  const animatedProcessed = useAnimatedNumber(processed);
  const animatedTotal = useAnimatedNumber(total);
  const pct =
    animatedTotal > 0
      ? Math.min(100, (animatedProcessed / animatedTotal) * 100)
      : 0;
  const indeterminate = total === 0;
  const trimmedScope = scope?.trim() ?? "";
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <div
        className={cn(
          "relative h-1 overflow-hidden rounded-full bg-border/60",
          compact && "h-[3px]",
        )}
        role="progressbar"
        aria-valuenow={indeterminate ? undefined : pct}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        {indeterminate ? (
          <div
            className="absolute inset-y-0 w-1/3 rounded-full bg-primary/80"
            style={{ animation: "ticker-bead 1.6s ease-in-out infinite" }}
          />
        ) : (
          <div
            className="h-full rounded-full bg-primary transition-[width] duration-500 ease-out"
            style={{ width: `${pct}%` }}
          />
        )}
      </div>
      {!compact && (
        <div className="flex items-center justify-between gap-3 text-[11.5px] tabular-nums text-muted-foreground">
          <span className="truncate">
            {trimmedScope && (
              <span className="text-foreground">{trimmedScope}</span>
            )}
            {trimmedScope && (indeterminate || total > 0) && (
              <span className="px-1 text-muted-foreground/70">·</span>
            )}
            {indeterminate ? (
              "Discovering…"
            ) : (
              <>
                <span className="text-foreground">
                  {animatedProcessed.toLocaleString()}
                </span>{" "}
                / {animatedTotal.toLocaleString()}
              </>
            )}
          </span>
          {errors > 0 && (
            <span className="text-destructive">
              {errors} {errors === 1 ? "error" : "errors"}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
