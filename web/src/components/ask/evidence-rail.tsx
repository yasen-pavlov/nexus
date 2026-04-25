import type { ChunkPreview } from "@/lib/api-types";
import { cn } from "@/lib/utils";

import { EvidenceCard } from "./evidence-card";

export interface EvidenceRailProps {
  chunks: ChunkPreview[];
  highlightedDocID?: string;
  onActivate: (docID: string) => void;
}

/**
 * Sticky right-side rail (≥ md) of evidence cards. On narrow screens it
 * collapses to a horizontal scroll above the active turn so the user
 * still has a reference without losing thread real estate.
 */
export function EvidenceRail({
  chunks,
  highlightedDocID,
  onActivate,
}: Readonly<EvidenceRailProps>) {
  return (
    <aside
      aria-label="Evidence"
      className={cn(
        "flex flex-col gap-3",
        // Narrow: sit above as a horizontal scroll. Wide: sticky right.
        "max-md:flex-row max-md:overflow-x-auto max-md:snap-x max-md:pb-2",
        "md:sticky md:top-4 md:max-h-[calc(100vh-2rem)] md:overflow-y-auto md:pr-2",
      )}
    >
      <div className="flex items-center gap-2 max-md:hidden">
        <span className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
          Evidence
        </span>
        <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground tabular-nums">
          {chunks.length}
        </span>
      </div>
      {chunks.length === 0 ? (
        <EmptyEvidence />
      ) : (
        <div
          className={cn(
            "flex flex-col gap-2",
            "max-md:flex-row max-md:gap-3 max-md:[&>button]:min-w-[280px] max-md:[&>button]:snap-start",
          )}
        >
          {chunks.map((c, i) => (
            <EvidenceCard
              key={c.id}
              number={i + 1}
              chunk={c}
              flashed={highlightedDocID === c.id}
              onActivate={() => onActivate(c.id)}
            />
          ))}
        </div>
      )}
    </aside>
  );
}

function EmptyEvidence() {
  return (
    <div className="rounded-lg border border-dashed border-border bg-card/50 p-4 text-center text-[12.5px] text-muted-foreground">
      Run a question to see what backed it.
    </div>
  );
}
