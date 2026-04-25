import type { CSSProperties } from "react";

import { sourceMetaFor } from "@/components/source-meta";
import { SourceChip } from "@/components/source-chip";
import type { ChunkPreview } from "@/lib/api-types";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

export interface EvidenceCardProps {
  /** 1-based index. Matches CitationPill numbering. */
  number: number;
  chunk: ChunkPreview;
  flashed?: boolean;
  onActivate: () => void;
}

/**
 * One retrieved-chunk card in the evidence rail. Reuses the v0.3.0
 * search-card chassis: tonal left spine, no shadow, color-only hover
 * elevation. The whole card is a real button so keyboard users can tab
 * across the rail and Enter-activate.
 *
 * `flashed` is set briefly after the matching CitationPill is clicked;
 * a hue-keyed ring fades in/out via tailwind transitions.
 */
export function EvidenceCard({
  number,
  chunk,
  flashed,
  onActivate,
}: Readonly<EvidenceCardProps>) {
  const meta = sourceMetaFor(chunk.source);
  const hueStyle = {
    "--chip-hue": `var(${meta.colorVar})`,
  } as CSSProperties;

  return (
    <button
      type="button"
      data-chunk-id={chunk.id}
      onClick={onActivate}
      style={hueStyle}
      className={cn(
        "group relative w-full overflow-hidden rounded-lg border border-border bg-card text-left",
        "transition-[background-color,border-color,box-shadow,outline-color] duration-150",
        "hover:bg-card-hover hover:border-accent-foreground/20",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
        // Flash uses a hue-keyed ring that transitions in/out.
        flashed
          ? "ring-2 ring-offset-2 ring-offset-background ring-[color:var(--chip-hue)]"
          : "ring-0 ring-offset-0",
      )}
    >
      <span
        aria-hidden
        style={{ background: `var(${meta.colorVar}, var(--source-default))` }}
        className="absolute inset-y-2 left-0 w-[3px] rounded-r-full opacity-70"
      />
      <div className="flex min-w-0 flex-col gap-1.5 p-3.5 pl-5">
        <div className="flex items-center gap-2">
          <span
            className={cn(
              "inline-flex h-5 min-w-5 items-center justify-center rounded-md px-1 text-[12px] font-semibold tabular-nums leading-none",
              "bg-[color-mix(in_oklch,var(--chip-hue)_14%,transparent)] text-[color:var(--chip-hue)]",
            )}
          >
            {number}
          </span>
          <SourceChip type={chunk.source} variant="default" />
          <div className="ml-auto text-[11px] text-muted-foreground">
            {chunk.date ? formatRelative(chunk.date) : ""}
          </div>
        </div>
        <div className="line-clamp-2 text-[14px] font-medium leading-[20px] text-foreground">
          {chunk.title || chunk.id}
        </div>
        {chunk.headline && (
          <p
            className={cn(
              "line-clamp-3 text-[12.5px] leading-[18px] text-muted-foreground",
              "[&_em]:rounded-sm [&_em]:bg-primary/15 [&_em]:px-0.5 [&_em]:font-medium [&_em]:not-italic [&_em]:text-foreground",
              "[&_mark]:rounded-sm [&_mark]:bg-primary/15 [&_mark]:px-0.5 [&_mark]:font-medium [&_mark]:text-foreground",
            )}
            dangerouslySetInnerHTML={{ __html: chunk.headline }}
          />
        )}
      </div>
    </button>
  );
}
