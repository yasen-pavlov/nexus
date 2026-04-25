import { useMemo } from "react";

import { TooltipProvider } from "@/components/ui/tooltip";
import type { ChatCitation, ChunkPreview } from "@/lib/api-types";
import { segmentAnswer } from "@/lib/segment-answer";
import { cn } from "@/lib/utils";

import { CitationPill } from "./citation-pill";

export interface AnswerStreamProps {
  text: string;
  citations: ChatCitation[];
  evidence: ChunkPreview[];
  isStreaming: boolean;
  onJumpToEvidence: (docID: string) => void;
}

const UNKNOWN_NUMBER = -1;

/**
 * Streamed answer prose. Citations resolve into inline tonal pills via
 * the pure segmentAnswer() helper; out-of-range citations defer until
 * later text deltas push the cursor past their span_end. After the
 * answer body, a small "Sources" rail offers a numeric mini-toc so
 * readers can jump even when a chunk wasn't cited inline.
 */
export function AnswerStream({
  text,
  citations,
  evidence,
  isStreaming,
  onJumpToEvidence,
}: Readonly<AnswerStreamProps>) {
  const numberByDocID = useMemo(() => {
    const map = new Map<string, number>();
    evidence.forEach((c, i) => map.set(c.id, i + 1));
    return map;
  }, [evidence]);

  const evidenceByDocID = useMemo(() => {
    const map = new Map<string, ChunkPreview>();
    for (const c of evidence) map.set(c.id, c);
    return map;
  }, [evidence]);

  // Only segment on citations whose doc_id has matching evidence.
  // Persisted assistant turns reload with citations but no evidence
  // (chunks aren't stored on the message row), so unresolvable
  // citations would otherwise render as "?" pills.
  const resolvableCitations = useMemo(
    () => citations.filter((c) => evidenceByDocID.has(c.doc_id)),
    [citations, evidenceByDocID],
  );

  const segments = useMemo(
    () => segmentAnswer(text, resolvableCitations),
    [text, resolvableCitations],
  );

  return (
    <TooltipProvider delay={250}>
      <div
        role="article"
        aria-label="Answer"
        aria-live="off"
        className="text-[15px] leading-[26px] tracking-[-0.003em] text-foreground"
      >
        {segments.map((seg, i) => {
          if (seg.kind === "text") {
            // Stable across renders — the text segment's identity is its
            // ordinal position; while streaming, only the final text
            // segment changes content.
            return <span key={`t-${i}`}>{seg.text}</span>;
          }
          const c = seg.citation;
          // Filtered above to only include citations with matching
          // evidence; the get() returning undefined would be a
          // contract violation, but we still defend with the unknown
          // fallback rather than crashing.
          const num = numberByDocID.get(c.doc_id) ?? UNKNOWN_NUMBER;
          const evidenceItem = evidenceByDocID.get(c.doc_id);
          return (
            <CitationPill
              key={`p-${c.doc_id}-${c.span_start}-${c.span_end}`}
              number={num}
              sourceType={evidenceItem?.source ?? ""}
              title={evidenceItem?.title ?? c.doc_id}
              citedText={c.cited_text}
              onClick={() => onJumpToEvidence(c.doc_id)}
            />
          );
        })}
        {isStreaming && (
          <span
            aria-hidden
            className="ml-[2px] inline-block h-[1em] w-[1ch] translate-y-[2px] animate-pulse bg-foreground/70"
          />
        )}
      </div>

      {evidence.length > 0 && (
        <div className={cn("mt-5 flex flex-col gap-1.5")}>
          <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
            Sources
          </div>
          <div className="flex flex-wrap gap-1.5">
            {evidence.map((c, i) => (
              <CitationPill
                key={`source-${c.id}`}
                number={i + 1}
                sourceType={c.source}
                title={c.title}
                onClick={() => onJumpToEvidence(c.id)}
              />
            ))}
          </div>
        </div>
      )}
    </TooltipProvider>
  );
}
