import { cn } from "@/lib/utils";

export interface SyncProgressProps {
  processed: number;
  total: number;
  errors?: number;
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
  compact,
  className,
}: SyncProgressProps) {
  const pct = total > 0 ? Math.min(100, (processed / total) * 100) : 0;
  const indeterminate = total === 0;
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
        <div className="flex items-center justify-between text-[11.5px] tabular-nums text-muted-foreground">
          <span>
            {indeterminate ? (
              "Discovering…"
            ) : (
              <>
                <span className="text-foreground">{processed.toLocaleString()}</span> /{" "}
                {total.toLocaleString()}
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
