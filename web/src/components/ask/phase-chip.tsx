import { useEffect, useState } from "react";
import { AlertCircle } from "lucide-react";

import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { StreamPhase } from "@/hooks/use-chat-stream";

export interface PhaseChipProps {
  phase: StreamPhase;
  /** The user's literal message — used to detect whether `query`
   *  represents a rewritten form vs. the original phrasing. */
  userContent: string;
  /** The query that actually went to OpenSearch (= rewritten when the
   *  rewriter ran, otherwise undefined or equal to userContent). */
  query?: string;
  /** True when the rewriter judged retrieval unnecessary; flips the
   *  chip into the muted "Answering from history" variant. */
  skippedRetrieval?: boolean;
  /** Wall-clock timestamp (ms) when the streaming turn started. When
   *  set, the chip renders an elapsed counter ("Searching · 0:23")
   *  that ticks every second so users have a sense of progress on
   *  slow models like reasoning-tier GPT-5. */
  startedAtMs?: number;
  /** When set, the rewriter ran but couldn't deliver a usable result.
   *  The chip stays in its default-marmalade "Searching your corpus"
   *  variant but adds a small AlertCircle glyph that explains the
   *  fallback on hover. Distinguishes "rewriter timed out" from
   *  "rewriter chose not to rewrite". */
  rewriterFailureReason?: string;
}

const FAILURE_LABELS: Record<string, string> = {
  timeout: "Rewriter timed out — searching with your original phrasing.",
  empty: "Rewriter returned an empty response — searching with your original phrasing.",
  parse_failed:
    "Rewriter output couldn't be parsed — searching with your original phrasing.",
  error:
    "Rewriter call failed — searching with your original phrasing.",
};

function formatElapsed(ms: number): string {
  const totalSec = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

/** Tick once per second while `startedAtMs` is set, returning the
 *  formatted elapsed string. Returns null when no timer is active. */
function useElapsed(startedAtMs?: number): string | null {
  const [now, setNow] = useState<number>(() => Date.now());
  useEffect(() => {
    if (!startedAtMs) return;
    const tick = () => setNow(Date.now());
    tick();
    const id = globalThis.setInterval(tick, 1000);
    return () => globalThis.clearInterval(id);
  }, [startedAtMs]);
  if (!startedAtMs) return null;
  return formatElapsed(now - startedAtMs);
}

/** Small marmalade-tinted glyph that explains a rewriter fallback on
 *  hover. Uses the project's Base-UI Tooltip primitives so the
 *  affordance matches the rest of the app. */
function FallbackHint({ reason }: Readonly<{ reason: string }>) {
  const label = FAILURE_LABELS[reason] ?? FAILURE_LABELS.error;
  return (
    <TooltipProvider delay={150}>
      <Tooltip>
        <TooltipTrigger
          render={
            <button
              type="button"
              aria-label="Rewriter status"
              className="inline-flex size-3.5 items-center justify-center rounded-full text-primary/70 hover:text-primary"
            >
              <AlertCircle className="size-3.5" aria-hidden />
            </button>
          }
        />
        <TooltipContent
          side="top"
          align="start"
          className="max-w-[260px] text-[11px] leading-[1.4]"
        >
          {label}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

/**
 * The streaming-turn phase chip. Three variants share one shape and one
 * pulsing-dot cadence; hue and content carry the meaning. As of Phase 4
 * polish: each variant also surfaces (a) an elapsed counter while a
 * turn is in flight so slow reasoning models (GPT-5 et al.) feel alive,
 * and (b) a quiet diagnostic glyph when the rewriter fell back, so
 * "Searching your corpus" no longer ambiguously covers "no rewrite
 * needed" vs. "rewriter timed out".
 *
 * Variants:
 *   1. Default (marmalade): "Searching your corpus" / "Generating answer"
 *   2. Rewritten (marmalade with thin vertical rule): "Searching | <q>"
 *   3. Skipped (muted): "Answering from history"
 */
export function PhaseChip({
  phase,
  userContent,
  query,
  skippedRetrieval,
  startedAtMs,
  rewriterFailureReason,
}: Readonly<PhaseChipProps>) {
  const elapsed = useElapsed(startedAtMs);

  if (skippedRetrieval) {
    return (
      <div
        role="status"
        aria-live="polite"
        className="inline-flex items-center gap-2 self-start rounded-full border border-border/40 bg-muted/40 px-3 py-1 text-[11px] font-medium text-muted-foreground"
      >
        <span
          className="size-1.5 animate-pulse rounded-full bg-muted-foreground/60"
          aria-hidden
        />
        <span>Answering from history</span>
        {elapsed && (
          <span className="font-normal text-muted-foreground/70 tabular-nums">
            · {elapsed}
          </span>
        )}
      </div>
    );
  }

  // Rewritten chip persists through both retrieving and streaming so
  // the user sees "what the system actually thought I meant" for the
  // full duration of the turn — transparency is most useful while the
  // answer is rendering, not just for the brief retrieval window.
  const isRewritten =
    (phase === "retrieving" || phase === "streaming") &&
    !!query &&
    query !== userContent;

  if (isRewritten) {
    return (
      <div
        role="status"
        aria-live="polite"
        className="inline-flex max-w-2xl items-center gap-2 self-start rounded-full bg-primary/10 px-3 py-1 text-[11px] font-medium text-primary"
      >
        <span
          className="size-1.5 animate-pulse rounded-full bg-primary"
          aria-hidden
        />
        <span>Searching</span>
        <span aria-hidden className="h-3 w-px bg-primary/30" />
        <span className="truncate font-normal italic text-primary/85">
          {query}
        </span>
        {elapsed && (
          <span className="font-normal text-primary/65 tabular-nums">
            · {elapsed}
          </span>
        )}
      </div>
    );
  }

  const phaseLabel =
    phase === "retrieving"
      ? "Searching your corpus"
      : phase === "streaming"
        ? "Generating answer"
        : "";
  if (!phaseLabel) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      className="inline-flex items-center gap-2 self-start rounded-full bg-primary/10 px-3 py-1 text-[11px] font-medium text-primary"
    >
      <span
        className="size-1.5 animate-pulse rounded-full bg-primary"
        aria-hidden
      />
      <span>{phaseLabel}</span>
      {rewriterFailureReason && <FallbackHint reason={rewriterFailureReason} />}
      {elapsed && (
        <span className="font-normal text-primary/65 tabular-nums">
          · {elapsed}
        </span>
      )}
    </div>
  );
}
