import type { CSSProperties } from "react";

import { sourceMetaFor } from "@/components/source-meta";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

export interface CitationPillProps {
  number: number;
  sourceType: string;
  title: string;
  citedText?: string;
  onClick: () => void;
}

const TOOLTIP_TEXT_CAP = 200;
const UNKNOWN_NUMBER = -1;

/**
 * Tonal numbered chip rendered inline inside the answer prose. The hue
 * is keyed off `sourceType` via the design-system color-mix pattern so
 * the pill reads as belonging to its source's color family.
 *
 * `number = -1` means "no matching evidence chunk" — defensively render
 * a "?" pill in muted styling so the prose doesn't crash on an
 * out-of-band citation.
 */
export function CitationPill({
  number,
  sourceType,
  title,
  citedText,
  onClick,
}: Readonly<CitationPillProps>) {
  const meta = sourceMetaFor(sourceType);
  const unknown = number === UNKNOWN_NUMBER;

  const hueStyle: CSSProperties = unknown
    ? {}
    : ({ "--chip-hue": `var(${meta.colorVar})` } as CSSProperties);

  const trimmed =
    citedText && citedText.length > TOOLTIP_TEXT_CAP
      ? citedText.slice(0, TOOLTIP_TEXT_CAP - 1) + "…"
      : citedText;

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <button
            type="button"
            onClick={onClick}
            aria-label={`Citation ${unknown ? "unknown" : number} — ${title}`}
            style={hueStyle}
            className={cn(
              "ml-[0.4em] inline-flex h-[1.125rem] min-w-[1.125rem] items-center justify-center rounded-md px-1 align-baseline font-semibold tabular-nums",
              "text-[11px] leading-none transition-colors",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-1 focus-visible:ring-offset-background focus-visible:ring-ring/40",
              unknown
                ? "bg-muted text-muted-foreground hover:bg-muted/80"
                : "bg-[color-mix(in_oklch,var(--chip-hue)_14%,transparent)] text-[color:var(--chip-hue)] hover:bg-[color-mix(in_oklch,var(--chip-hue)_22%,transparent)]",
            )}
          >
            {unknown ? "?" : number}
          </button>
        }
      />
      <TooltipContent side="top" align="center">
        <div className="flex max-w-xs flex-col gap-1 text-left">
          <div className="text-[10px] font-semibold uppercase tracking-[0.08em] opacity-80">
            {meta.label}
          </div>
          <div className="text-[12px] font-medium">{title}</div>
          {trimmed && (
            <div className="text-[11px] leading-snug opacity-90">{trimmed}</div>
          )}
          <div className="text-[10px] opacity-60">Click to jump</div>
        </div>
      </TooltipContent>
    </Tooltip>
  );
}
