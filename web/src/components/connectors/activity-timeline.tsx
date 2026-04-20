import { format, formatDistanceStrict } from "date-fns";

import { cn } from "@/lib/utils";
import type { SyncRun } from "@/lib/api-types";
import { StatusLamp } from "./status-lamp";
import { statusFromSync, type ConnectorStatus } from "./status";

export function ActivityTimeline({ runs }: Readonly<{ runs: SyncRun[] }>) {
  if (runs.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border bg-card p-8 text-center">
        <p className="text-[13px] text-muted-foreground">
          No sync runs yet. Trigger a sync to see history here.
        </p>
      </div>
    );
  }
  return (
    <ol className="relative">
      <span aria-hidden className="absolute left-[11px] bottom-2 top-2 w-px bg-border" />
      {runs.map((run) => {
        const status: ConnectorStatus = statusFromSync(run.status, "idle");
        const started = new Date(run.started_at);
        const completed = run.completed_at ? new Date(run.completed_at) : null;
        return (
          <li key={run.id} className="relative flex gap-4 py-3 pl-0 pr-4">
            <span
              className={cn(
                "relative z-10 mt-1 flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full border bg-card",
                status === "failed" && "border-destructive/50",
                status === "succeeded" && "border-[color:oklch(0.70_0.12_155)]",
                status === "running" && "border-primary/50",
                status === "canceled" && "border-border",
                status === "interrupted" && "border-[color:oklch(0.72_0.10_70)]/50",
              )}
            >
              <StatusLamp status={status} />
            </span>
            <div className="flex-1">
              <div className="flex items-baseline justify-between gap-3">
                <div className="text-[13px] font-medium text-foreground">
                  {format(started, "MMM d · HH:mm")}
                </div>
                <div className="text-[12px] tabular-nums text-muted-foreground">
                  {completed ? formatDistanceStrict(started, completed) : "running"}
                </div>
              </div>
              <div className="mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 text-[12px] text-muted-foreground">
                <span className="tabular-nums">
                  {run.docs_processed.toLocaleString()} indexed
                </span>
                {run.docs_deleted > 0 && (
                  <span className="tabular-nums">
                    {run.docs_deleted.toLocaleString()} removed
                  </span>
                )}
                {run.errors > 0 && (
                  <span className="tabular-nums text-destructive">
                    {run.errors} {run.errors === 1 ? "error" : "errors"}
                  </span>
                )}
              </div>
              {(status === "failed" || status === "interrupted") && run.error_message && (
                <pre className="mt-2 overflow-auto rounded-md border border-destructive/20 bg-destructive/5 p-2 font-mono text-[11.5px] leading-relaxed text-destructive/90">
                  {run.error_message}
                </pre>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
