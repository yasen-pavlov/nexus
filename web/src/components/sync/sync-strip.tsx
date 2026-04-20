import { Link } from "@tanstack/react-router";
import { Loader2 } from "lucide-react";
import { formatDistanceToNowStrict } from "date-fns";

import { cn } from "@/lib/utils";

export interface SyncStripStats {
  sourceCount: number;
  lastSyncAt?: string;
}

export interface SyncStripRunningJob {
  connectorName: string;
  processed: number;
  total: number;
}

export interface SyncStripProps {
  stats: SyncStripStats;
  running: SyncStripRunningJob[];
  totalActive: number;
}

/**
 * Top-bar telegraph. Quiet stats when idle, gliding fill + running label
 * when a sync is live. Clickable from any page → /connectors.
 *
 * Aggregate fill = average of (processed / total) across all running jobs,
 * which tracks total progress without over-weighting the loudest one.
 * The leader label shows the first running job's name + its own %; this
 * is the one the user most likely kicked off themselves.
 */
export function SyncStrip({ stats, running, totalActive }: Readonly<SyncStripProps>) {
  const idle = running.length === 0;
  const leader = running[0];

  const aggregatePct = idle
    ? 0
    : running.reduce((acc, r) => acc + (r.total > 0 ? r.processed / r.total : 0), 0) /
      running.length;

  return (
    <Link
      to="/connectors"
      className={cn(
        "group relative flex h-8 items-center overflow-hidden rounded-full border border-border bg-card px-3 text-[12px] text-muted-foreground transition-colors hover:bg-card-hover",
        !idle && "border-primary/40 text-foreground",
      )}
      aria-label={idle ? "View connectors" : `Syncing ${running.length} of ${totalActive}`}
    >
      {!idle && (
        <span
          aria-hidden
          className="pointer-events-none absolute inset-y-0 left-0 bg-primary/10 transition-[width]"
          style={{ width: `${Math.min(100, aggregatePct * 100)}%` }}
        />
      )}

      <span className="relative flex items-center gap-3 tabular-nums">
        {idle ? (
          <>
            <span>
              <span className="tabular-nums text-foreground/80">{stats.sourceCount}</span>{" "}
              {stats.sourceCount === 1 ? "source" : "sources"}
            </span>
            {stats.lastSyncAt && (
              <>
                <Dot />
                <span>
                  synced{" "}
                  <span className="tabular-nums text-foreground/80">
                    {formatDistanceToNowStrict(new Date(stats.lastSyncAt))}
                  </span>{" "}
                  ago
                </span>
              </>
            )}
          </>
        ) : (
          <>
            <Loader2 className="h-3 w-3 animate-spin text-primary" />
            <span className="font-medium text-foreground">
              Syncing {totalActive > 1 ? `${running.length}/${totalActive}` : "1"}
            </span>
            <Dot />
            <span className="truncate">
              <span className="text-foreground">{leader.connectorName}</span>
              {leader.total > 0 && (
                <> · {Math.round((leader.processed / leader.total) * 100)}%</>
              )}
            </span>
          </>
        )}
      </span>
    </Link>
  );
}

function Dot() {
  return <span className="opacity-40">·</span>;
}
