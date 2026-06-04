import { useId, useMemo, useState, type ReactNode } from "react";
import { ChevronRight, Search } from "lucide-react";

import type { ChunkPreview } from "@/lib/api-types";
import { cn } from "@/lib/utils";

import { EvidenceCard } from "./evidence-card";

export interface ToolTraceProps {
  name: string;
  args: string;
  summary?: string;
  chunks?: ChunkPreview[];
  defaultExpanded?: boolean;
}

/**
 * Surfaces ONE search the agent ran during this turn — the
 * orchestrator's automatic first search and any model-issued
 * `nexus_search` calls all render with the same uniform "Searched
 * <query>" label. Multiple instances stack vertically when the agent
 * iterated. Treated as supportive transparency, not the answer — the
 * editorial-footnote aesthetic (thin marmalade rule from collapsed
 * strip down to the expanded chunk list) keeps it visually subordinate
 * to the prose.
 *
 * Inside the expanded body, EvidenceCards are passive previews —
 * clicking them re-invokes the strip's local toggle (no global rail
 * to jump to). Cited chunks live in the per-turn AnswerStream Sources
 * footer; the strips are for "what the system looked at, cited or not".
 */
export function ToolTrace({
  args,
  summary,
  chunks,
  defaultExpanded = false,
}: Readonly<ToolTraceProps>) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const regionId = useId();

  const derivedQuery = useMemo(() => extractQuery(args), [args]);
  // The orchestrator's automatic first search labels its strip with the
  // literal user question when no rewriter ran — that can be a long
  // sentence, unlike the model's own terse nexus_search queries. Cap the
  // visible label at a word boundary so every strip reads as a tidy
  // query; the full text stays on hover via `title`.
  const displayQuery = derivedQuery ? truncateQuery(derivedQuery) : undefined;
  // While the matching tool_result hasn't landed yet (no summary set),
  // pulse the search glyph so the live state reads as "in flight"
  // without a second loading affordance.
  const pending = summary === undefined && !chunks;
  const hasChunks = (chunks?.length ?? 0) > 0;

  let label: ReactNode;
  if (summary) {
    label = summary;
  } else if (displayQuery) {
    label = (
      <>
        Searched{" "}
        <span
          className="italic font-normal text-muted-foreground"
          title={derivedQuery === displayQuery ? undefined : derivedQuery}
        >
          {displayQuery}
        </span>
      </>
    );
  } else {
    label = "Searched the index";
  }

  return (
    <div className="flex flex-col">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        aria-controls={regionId}
        className={cn(
          "group inline-flex w-full items-center gap-2.5 self-start rounded-lg border px-3 py-2 text-left",
          "border-border/40 bg-muted/40 hover:bg-muted/60",
          "transition-colors duration-150",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
        )}
      >
        <Search
          aria-hidden
          className={cn(
            "size-3.5 shrink-0 text-primary",
            pending && "animate-pulse",
          )}
        />
        <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium text-foreground">
          {label}
        </span>
        {hasChunks && (
          <span
            aria-hidden
            className="rounded-full bg-primary/10 px-1.5 py-px text-[10.5px] font-semibold tabular-nums text-primary/80"
          >
            {chunks!.length}
          </span>
        )}
        <ChevronRight
          aria-hidden
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground/70 transition-transform duration-200",
            expanded && "rotate-90",
          )}
        />
      </button>

      <div
        id={regionId}
        role="region"
        className={cn(
          "transition-[max-height,opacity] duration-200 ease-out",
          expanded
            ? "max-h-[2000px] opacity-100"
            : "pointer-events-none max-h-0 opacity-0 overflow-hidden",
        )}
      >
        <div className="relative mt-2 pl-4">
          <span
            aria-hidden
            className="absolute left-2 top-0 bottom-0 w-px bg-primary/30"
          />
          {hasChunks ? (
            <div className="flex flex-col gap-2">
              {chunks!.map((chunk, idx) => (
                <EvidenceCard
                  key={chunk.id}
                  number={idx + 1}
                  chunk={chunk}
                  // No global rail to jump to — clicking the card is
                  // a passive collapse/expand of the parent strip.
                  // Keeps the affordance feeling alive without
                  // introducing a navigation surprise.
                  onActivate={() => setExpanded(false)}
                />
              ))}
            </div>
          ) : (
            <p className="text-[12px] italic text-muted-foreground">
              No matching documents.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}

/**
 * Pull `query` out of the model's raw tool-call args JSON, wrapped in
 * quotes for display. Tolerant — malformed JSON, missing field, or
 * non-string field all collapse to undefined so the caller can fall
 * back to a generic label.
 */
function extractQuery(args: string): string | undefined {
  if (!args) return undefined;
  try {
    const parsed = JSON.parse(args) as { query?: unknown };
    if (typeof parsed.query === "string" && parsed.query.trim() !== "") {
      const q = parsed.query.trim();
      return `"${q}"`;
    }
  } catch {
    // Malformed JSON — fall through.
  }
  return undefined;
}

const QUERY_LABEL_CAP = 64;

/**
 * Shorten a quoted query label to a word boundary near QUERY_LABEL_CAP,
 * appending an ellipsis inside the closing quote ("foo bar…"). Returns
 * the input unchanged when it already fits. Keeps the long initial-
 * retrieval strip (labeled with the verbatim user question) as tidy as
 * the model's own terse nexus_search labels.
 */
function truncateQuery(quoted: string): string {
  // Operate on the inner text so the wrapping quotes are preserved.
  const inner = quoted.replace(/^"|"$/g, "");
  if (inner.length <= QUERY_LABEL_CAP) return quoted;
  const slice = inner.slice(0, QUERY_LABEL_CAP);
  const lastSpace = slice.lastIndexOf(" ");
  // Prefer a word boundary, but don't chop off most of the cap chasing
  // one — fall back to a hard cut when the last space is very early.
  const cut = lastSpace > QUERY_LABEL_CAP * 0.6 ? lastSpace : slice.length;
  return `"${inner.slice(0, cut).trimEnd()}…"`;
}
