import { AlertCircle, Copy, RotateCcw } from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import type { ChatMessage, ChunkPreview } from "@/lib/api-types";
import type { StreamingTurn } from "@/hooks/use-chat-stream";
import { splitModelID } from "@/lib/llm-catalog";
import { cn } from "@/lib/utils";

import { AnswerStream } from "./answer-stream";
import { buildCopyText } from "./copy-text";

export interface AssistantTurnProps {
  /** Persisted assistant message — present when this turn is finished
   *  and stored in chat_messages. */
  message?: ChatMessage;
  /** In-flight streaming state. Mutually exclusive with `message`. */
  streaming?: StreamingTurn;
  evidence: ChunkPreview[];
  onJumpToEvidence: (docID: string) => void;
  onRegenerate?: () => void;
  /** ISO timestamp of the user message this assistant turn answered.
   *  Used to derive a wall-clock duration for the metadata footer
   *  ("13.4s · 1.2k in · 477 out") so users can compare model latency
   *  across turns. Optional — when absent the duration is omitted. */
  prevTurnCreatedAt?: string;
}

interface Resolved {
  text: string;
  citations: ChatMessage["citations"];
  isStreaming: boolean;
  isError: boolean;
  errorMessage?: string;
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
      errorMessage: streaming.error?.message,
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
  onJumpToEvidence,
  onRegenerate,
  prevTurnCreatedAt,
}: Readonly<AssistantTurnProps>) {
  const [copied, setCopied] = useState(false);
  const r = useMemo(() => resolve({ message, streaming }), [message, streaming]);
  const modelLabel = r.modelID ? splitModelID(r.modelID).bare || r.modelID : "";
  // Wall-clock duration. Prefers the server-measured duration_ms
  // (carried on the `done` SSE frame for streaming, persisted on the
  // chat_messages row for refreshed views) so the label is stable
  // across page reload. Falls back to FE-side timestamps for messages
  // that predate migration 019. The phase chip handles the live ticker
  // DURING streaming; we only show this label after the turn finishes.
  let durationLabel = "";
  if (streaming) {
    if (typeof streaming.durationMs === "number") {
      durationLabel = formatDuration(streaming.durationMs);
    } else if (streaming.startedAt && streaming.completedAt) {
      durationLabel = formatDuration(
        streaming.completedAt - streaming.startedAt,
      );
    }
  } else if (!r.isStreaming && message) {
    if (typeof message.duration_ms === "number") {
      durationLabel = formatDuration(message.duration_ms);
    } else if (prevTurnCreatedAt) {
      durationLabel = formatDuration(
        new Date(message.created_at).getTime() -
          new Date(prevTurnCreatedAt).getTime(),
      );
    }
  }

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

  return (
    <article aria-label="Assistant turn" className="flex flex-col gap-1">
      <AnswerStream
        text={r.text}
        citations={r.citations ?? []}
        evidence={evidence}
        isStreaming={r.isStreaming}
        onJumpToEvidence={onJumpToEvidence}
      />

      {r.isError && (
        <div className="mt-4 flex items-start gap-2 rounded-lg border border-destructive/20 bg-destructive/8 p-3 text-[13px] text-destructive">
          <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden />
          <div className="flex-1">
            <div className="font-medium">Something went wrong</div>
            <div className="opacity-90">
              {r.errorMessage ?? "The provider returned an error."}
            </div>
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
        {r.isStreaming ? (
          <span className="inline-flex items-center gap-1.5 rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary">
            <span className="size-1.5 animate-pulse rounded-full bg-primary" aria-hidden />
            Live
          </span>
        ) : modelLabel ? (
          <span className="font-medium text-foreground/80">{modelLabel}</span>
        ) : null}

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
