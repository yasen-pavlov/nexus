import { AlertCircle, Copy, RotateCcw, ThumbsDown, ThumbsUp } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { useMessageFeedback, type Feedback } from "@/hooks/use-message-feedback";
import { Button } from "@/components/ui/button";
import type {
  ChatMessage,
  ChatToolEvent,
  ChunkPreview,
} from "@/lib/api-types";
import type { StreamingTurn } from "@/hooks/use-chat-stream";
import { splitModelID } from "@/lib/llm-catalog";
import { cn } from "@/lib/utils";

import { AnswerStream } from "./answer-stream";
import { buildCopyText } from "./copy-text";
import { ToolTrace } from "./tool-trace";

export interface AssistantTurnProps {
  /** Persisted assistant message — present when this turn is finished
   *  and stored in chat_messages. */
  message?: ChatMessage;
  /** In-flight streaming state. Mutually exclusive with `message`. */
  streaming?: StreamingTurn;
  /** Cited-only chunks — the per-turn Sources footer renders these as
   *  numbered chips, with the same numbering used by inline `[N]` pills. */
  evidence: ChunkPreview[];
  onRegenerate?: () => void;
  /** ISO timestamp of the user message this assistant turn answered.
   *  Used to derive a wall-clock duration for the metadata footer
   *  ("13.4s · 1.2k in · 477 out") so users can compare model latency
   *  across turns. Optional — when absent the duration is omitted. */
  prevTurnCreatedAt?: string;
  /** The user message content this turn answered. Used to label the
   *  synthetic "Searched <query>" strip when the rewriter didn't run
   *  (first turn or rewriter disabled) so the strip shows the literal
   *  question instead of the generic "Searched the index" fallback. */
  prevUserContent?: string;
}

interface Resolved {
  text: string;
  citations: ChatMessage["citations"];
  isStreaming: boolean;
  isError: boolean;
  modelID: string;
  inputTokens?: number;
  outputTokens?: number;
}

function resolve({
  message,
  streaming,
}: Pick<AssistantTurnProps, "message" | "streaming">): Resolved {
  if (streaming) {
    return {
      text: streaming.answer,
      citations: streaming.citations,
      isStreaming:
        streaming.phase === "retrieving" || streaming.phase === "streaming",
      isError: streaming.phase === "error",
      // Don't surface the raw provider error to users — the AssistantTurn
      // error block falls back to "The provider returned an error." which
      // matches the post-reload persisted view. Underlying message lives
      // in the BE WARN log + the Network tab SSE error frame for
      // diagnosis; the chat surface stays calm.
      modelID: "", // Surface the chosen model when stable on the persisted msg.
      inputTokens: streaming.usage?.input,
      outputTokens: streaming.usage?.output,
    };
  }
  if (message) {
    return {
      text: message.content,
      citations: message.citations ?? [],
      isStreaming: false,
      isError: message.stop_reason === "error",
      modelID: message.model ?? "",
      inputTokens: message.usage?.input,
      outputTokens: message.usage?.output,
    };
  }
  return {
    text: "",
    citations: [],
    isStreaming: false,
    isError: false,
    modelID: "",
  };
}

export function AssistantTurn({
  message,
  streaming,
  evidence,
  onRegenerate,
  prevTurnCreatedAt,
  prevUserContent,
}: Readonly<AssistantTurnProps>) {
  const [copied, setCopied] = useState(false);
  const r = useMemo(() => resolve({ message, streaming }), [message, streaming]);
  const modelLabel = r.modelID ? splitModelID(r.modelID).bare || r.modelID : "";

  // Per-search strips above the answer body. Each strip = one search
  // event. The orchestrator's automatic first search and any
  // model-issued nexus_search calls render with the same uniform
  // "Searched <query>" label — the user mental model is "show me
  // every search that fed this answer", not "who issued each call".
  //
  // Streaming source is the live `toolEvents` accumulator from the SSE
  // stream + the streaming turn's initial evidence. Persisted source
  // is `message.evidence` (initial = union minus tool-fetched) +
  // `message.tool_calls` (which denormalises per-call chunks).
  // Skip-retrieval turns produce no strip at all.
  const toolEvents = useMemo<ChatToolEvent[]>(() => {
    const out: ChatToolEvent[] = [];

    // Streaming branch. `streaming.evidence` is the cumulative union
    // (initial retrieval + every tool_result, folded in by use-chat-stream
    // so the Sources footer can resolve cited tool-fetched docs). For the
    // synthetic "initial retrieval" strip we want the INITIAL subset only,
    // so — exactly like the persisted branch below — remove every chunk
    // already attributed to a tool call. Without this the tool-fetched
    // chunks would show twice: once here and once in their own tool strip.
    if (streaming) {
      const toolChunkIDs = new Set<string>();
      for (const ev of streaming.toolEvents) {
        for (const c of ev.chunks ?? []) toolChunkIDs.add(c.id);
      }
      const initialChunks = streaming.evidence.filter(
        (c) => !toolChunkIDs.has(c.id),
      );
      if (!streaming.skippedRetrieval && initialChunks.length > 0) {
        out.push({
          name: "nexus_search",
          args: JSON.stringify({
            query: streaming.query ?? streaming.userContent,
          }),
          summary: undefined,
          chunks: initialChunks,
        });
      }
      out.push(...streaming.toolEvents);
      return out;
    }

    // Persisted branch. Compute the "initial" chunk subset by
    // removing every chunk attributed to a tool call from the
    // evidence union.
    if (message) {
      const toolChunkIDs = new Set<string>();
      const toolEventsList: ChatToolEvent[] = (message.tool_calls ?? []).map(
        (tc) => {
          for (const c of tc.chunks ?? []) toolChunkIDs.add(c.id);
          return {
            name: tc.name,
            args: tc.args,
            summary: tc.result_summary,
            chunks: tc.chunks,
          };
        },
      );

      const initialChunks = (message.evidence ?? []).filter(
        (c) => !toolChunkIDs.has(c.id),
      );
      if (!message.skipped_retrieval && initialChunks.length > 0) {
        // Prefer the rewriter's query when it ran; otherwise fall back
        // to the literal user question (passed in from chat-thread).
        // Either way the strip reads "Searched <query>" so users can
        // see exactly what hit the index.
        const labelQuery =
          message.rewritten_query?.trim() || prevUserContent?.trim() || "";
        out.push({
          name: "nexus_search",
          args: JSON.stringify({ query: labelQuery }),
          summary: undefined,
          chunks: initialChunks,
        });
      }
      out.push(...toolEventsList);
      return out;
    }

    return out;
  }, [streaming, message, prevUserContent]);
  const durationLabel = deriveDurationLabel({
    streaming,
    message,
    isStreaming: r.isStreaming,
    prevTurnCreatedAt,
  });

  const onCopy = async () => {
    const txt = buildCopyText(r.text, r.citations, evidence);
    try {
      await navigator.clipboard.writeText(txt);
      setCopied(true);
      globalThis.setTimeout(() => setCopied(false), 1500);
    } catch {
      // navigator.clipboard can fail on http (non-secure context) —
      // silently no-op rather than throwing.
    }
  };

  const showActions = !r.isStreaming && (r.text.length > 0 || r.isError);

  // Footer status indicator: a live pulse while streaming, the model
  // label once the turn settles, or nothing. Derived here rather than a
  // nested ternary in the footer JSX.
  let statusIndicator: ReactNode = null;
  if (r.isStreaming) {
    statusIndicator = (
      <span className="inline-flex items-center gap-1.5 rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary">
        <span
          className="size-1.5 animate-pulse rounded-full bg-primary"
          aria-hidden
        />
        {"Live"}
      </span>
    );
  } else if (modelLabel) {
    statusIndicator = (
      <span className="font-medium text-foreground/80">{modelLabel}</span>
    );
  }

  return (
    <article aria-label="Assistant turn" className="flex flex-col gap-1">
      {toolEvents.length > 0 && (
        <div className="mb-3 flex flex-col gap-2">
          {toolEvents.map((ev, idx) => (
            <ToolTrace
              key={`${ev.name}-${idx}`}
              name={ev.name}
              args={ev.args}
              summary={ev.summary}
              chunks={ev.chunks}
            />
          ))}
        </div>
      )}

      <AnswerStream
        text={r.text}
        citations={r.citations ?? []}
        evidence={evidence}
        isStreaming={r.isStreaming}
      />

      {r.isError && (
        <div className="mt-4 flex items-start gap-2 rounded-lg border border-destructive/20 bg-destructive/8 p-3 text-[13px] text-destructive">
          <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden />
          <div className="flex-1">
            <div className="font-medium">Something went wrong</div>
            <div className="opacity-90">The provider returned an error.</div>
          </div>
          {onRegenerate && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={onRegenerate}
              className="border-destructive/30 text-destructive hover:bg-destructive/10"
            >
              <RotateCcw className="size-3.5" aria-hidden />
              Retry
            </Button>
          )}
        </div>
      )}

      <footer
        className={cn(
          "mt-5 flex flex-wrap items-center gap-3 border-t border-border/60 pt-3",
          "text-[12px] text-muted-foreground",
        )}
      >
        {statusIndicator}

        {durationLabel && (
          <span className="tabular-nums" title="Wall-clock from your message to the assistant's done event">
            {durationLabel}
          </span>
        )}

        {(r.inputTokens !== undefined || r.outputTokens !== undefined) && (
          <span className="tabular-nums">
            {formatTokens(r.inputTokens)} in · {formatTokens(r.outputTokens)} out
          </span>
        )}

        {showActions && (
          <div className="ml-auto flex items-center gap-1">
            {/* Thumbs only on a persisted (non-error) assistant message —
                a rating needs a stored message id to attach to. */}
            {message && !r.isError && <FeedbackButtons message={message} />}
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={onCopy}
              className="h-7 gap-1.5 px-2 text-[11px]"
              aria-label={copied ? "Copied" : "Copy answer"}
            >
              <Copy className="size-3.5" aria-hidden />
              {copied ? "Copied" : "Copy"}
            </Button>
            {onRegenerate && (
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={onRegenerate}
                className="h-7 gap-1.5 px-2 text-[11px]"
                aria-label="Regenerate answer"
              >
                <RotateCcw className="size-3.5" aria-hidden />
                Regenerate
              </Button>
            )}
          </div>
        )}
      </footer>
    </article>
  );
}

/**
 * Thumbs up/down on a persisted assistant message. Clicking the active
 * rating again clears it. Optimistic: the local state flips immediately
 * and the mutation reconciles via a chat-detail refetch on success.
 */
function FeedbackButtons({ message }: Readonly<{ message: ChatMessage }>) {
  const fb = useMessageFeedback();
  const [rating, setRating] = useState<Feedback>(message.feedback ?? null);

  const choose = (value: "up" | "down") => {
    const next: Feedback = rating === value ? null : value;
    setRating(next);
    fb.mutate(
      { chatId: message.chat_id, messageId: message.id, feedback: next },
      { onError: () => setRating(message.feedback ?? null) },
    );
  };

  return (
    <div className="flex items-center" role="group" aria-label="Rate this answer">
      <Button
        type="button"
        size="sm"
        variant="ghost"
        onClick={() => choose("up")}
        aria-pressed={rating === "up"}
        aria-label="Good answer"
        className={cn(
          "h-7 w-7 px-0",
          rating === "up" && "text-primary",
        )}
      >
        <ThumbsUp className="size-3.5" aria-hidden />
      </Button>
      <Button
        type="button"
        size="sm"
        variant="ghost"
        onClick={() => choose("down")}
        aria-pressed={rating === "down"}
        aria-label="Bad answer"
        className={cn(
          "h-7 w-7 px-0",
          rating === "down" && "text-destructive",
        )}
      >
        <ThumbsDown className="size-3.5" aria-hidden />
      </Button>
    </div>
  );
}

/**
 * Wall-clock duration label for the metadata footer. Prefers the
 * server-measured duration_ms (carried on the `done` SSE frame for
 * streaming, persisted on the chat_messages row for refreshed views) so
 * the label is stable across page reload. Falls back to FE-side
 * timestamps for messages that predate migration 019. The phase chip
 * handles the live ticker DURING streaming; this label only shows after
 * the turn finishes.
 */
function deriveDurationLabel({
  streaming,
  message,
  isStreaming,
  prevTurnCreatedAt,
}: {
  streaming?: StreamingTurn;
  message?: ChatMessage;
  isStreaming: boolean;
  prevTurnCreatedAt?: string;
}): string {
  if (streaming) {
    if (typeof streaming.durationMs === "number") {
      return formatDuration(streaming.durationMs);
    }
    if (streaming.startedAt && streaming.completedAt) {
      return formatDuration(streaming.completedAt - streaming.startedAt);
    }
    return "";
  }
  if (!isStreaming && message) {
    if (typeof message.duration_ms === "number") {
      return formatDuration(message.duration_ms);
    }
    if (prevTurnCreatedAt) {
      return formatDuration(
        new Date(message.created_at).getTime() -
          new Date(prevTurnCreatedAt).getTime(),
      );
    }
  }
  return "";
}

function formatTokens(n: number | undefined): string {
  if (n === undefined) return "—";
  if (n < 1000) return String(n);
  return `${(n / 1000).toFixed(1)}k`;
}

/** Format a millisecond duration as a tight humanised string:
 *  - sub-second values render as ms ("420ms")
 *  - sub-minute values render as decimal seconds ("13.4s")
 *  - minute-or-more values render as "m:ss" ("1:23")
 *  Returns empty string for non-positive values so the metadata row
 *  doesn't render a misleading "0ms". */
function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const sec = ms / 1000;
  if (sec < 60) return `${sec.toFixed(1)}s`;
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${m}:${String(s).padStart(2, "0")}`;
}
