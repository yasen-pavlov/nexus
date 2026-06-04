// Streaming-turn state machine for /api/chats/{id}/messages.
//
// Folds named SSE frames (retrieving / evidence / text / citation /
// usage / done / error) into a typed StreamingTurn. Distinguishes
// transport errors (network drop, 5xx, fetch reject) from protocol
// errors (the BE emits an `event: error` frame). Swallows AbortError
// on user-initiated cancel. On unmount mid-stream, persists the
// in-flight turn into the TanStack Query cache so navigating away and
// back doesn't lose what was rendered.

import { useCallback, useEffect, useReducer, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";

import {
  openChatMessageStream,
  type SSEFrame,
} from "@/lib/api-client";
import type {
  ChatCitation,
  ChatToolEvent,
  ChatUsage,
  ChunkPreview,
} from "@/lib/api-types";
import { chatKeys } from "@/lib/query-keys";

export type StreamPhase =
  | "idle"
  | "retrieving"
  | "streaming"
  | "done"
  | "error";

export interface StreamErrorInfo {
  /** transport = network/HTTP failure; protocol = BE `error` frame. */
  kind: "transport" | "protocol";
  message: string;
}

export interface StreamingTurn {
  phase: StreamPhase;
  /** The user content that started this turn — useful for "Regenerate". */
  userContent: string;
  /** The query that actually went to OpenSearch — equals userContent when
   *  the rewriter didn't run, otherwise the rewriter's normalised form.
   *  The phase chip shows "Searching for: <query>" when query !== userContent. */
  query?: string;
  evidence: ChunkPreview[];
  answer: string;
  citations: ChatCitation[];
  usage?: ChatUsage;
  stopReason?: string;
  messageID?: string;
  error?: StreamErrorInfo;
  /** True when the rewriter judged the question answerable from chat
   *  history alone — phase strip flips to "Answering from history" and
   *  the evidence rail collapses for this turn. */
  skippedRetrieval?: boolean;
  /** Auto-title delivered via `event: title` between usage and done.
   *  Consumed by chat-thread.tsx to invalidate chatKeys.list() so the
   *  recent-chats grid catches the new title without a manual refetch. */
  autoTitle?: string;
  /** When set, the rewriter ran but couldn't produce a usable result
   *  (timeout / empty / parse_failed / error). The phase chip renders
   *  a quiet diagnostic glyph so users know the search ran with their
   *  literal phrasing rather than a coreference-resolved rewrite. */
  rewriterFailureReason?: string;
  /** When set, the auto-title path attempted but failed. Surfaced to
   *  the same diagnostic affordance so admins can see why titles
   *  aren't appearing on chats they expected to see auto-titled. */
  titleFailureReason?: string;
  /** Wall-clock timestamp (ms since epoch) when the turn started.
   *  PhaseChip uses it to render an elapsed counter during streaming
   *  ("Generating answer · 0:23"); unset on idle. */
  startedAt?: number;
  /** Wall-clock timestamp (ms since epoch) when the turn completed
   *  (done or error frame received). Paired with startedAt so the
   *  streaming AssistantTurn footer can show a final duration label
   *  the instant the turn ends — without waiting for the chat-detail
   *  refetch that would surface persisted timestamps. */
  completedAt?: number;
  /** Server-measured runTurn duration in milliseconds, carried on the
   *  `done` SSE frame. Preferred over (completedAt − startedAt) for
   *  display because it matches the persisted value: the FE pair
   *  includes network + SSE-flush latency and would drift after a
   *  page refresh. */
  durationMs?: number;
  /** Phase 5: per-round agentic tool-call trace. One entry per
   *  `tool_start` SSE frame, populated with summary + chunks when the
   *  matching `tool_result` lands. AssistantTurn renders these as
   *  collapsible ToolTrace rows above the answer body. */
  toolEvents: ChatToolEvent[];
}

const INITIAL: StreamingTurn = {
  phase: "idle",
  userContent: "",
  evidence: [],
  answer: "",
  citations: [],
  toolEvents: [],
};

type Action =
  | { type: "start"; userContent: string }
  | { type: "frame"; frame: SSEFrame }
  | { type: "transport_error"; message: string }
  | { type: "reset" }
  | { type: "hydrate"; turn: StreamingTurn };

function reducer(state: StreamingTurn, action: Action): StreamingTurn {
  switch (action.type) {
    case "start":
      return {
        ...INITIAL,
        userContent: action.userContent,
        phase: "retrieving",
        startedAt: Date.now(),
      };
    case "transport_error":
      return {
        ...state,
        phase: "error",
        error: { kind: "transport", message: action.message },
        completedAt: Date.now(),
      };
    case "reset":
      return INITIAL;
    case "hydrate":
      return action.turn;
    case "frame":
      return applyFrame(state, action.frame);
  }
}

function applyCitation(state: StreamingTurn, payload: unknown): StreamingTurn {
  const c = payload as Partial<{
    doc_id: string;
    cited_text: string;
    span: [number, number];
  }>;
  if (!c.doc_id || !Array.isArray(c.span)) return state;
  const citation: ChatCitation = {
    doc_id: c.doc_id,
    cited_text: c.cited_text,
    span_start: c.span[0],
    span_end: c.span[1],
  };
  return { ...state, citations: [...state.citations, citation] };
}

function applyToolResult(state: StreamingTurn, payload: unknown): StreamingTurn {
  const p = payload as {
    name?: string;
    summary?: string;
    chunks?: ChunkPreview[];
  };
  if (!p.name) return state;
  // Merge onto the most recent same-name event whose summary hasn't
  // been set yet. If none exists (server sent tool_result without a
  // matching tool_start — should not happen but tolerate), drop.
  let merged = false;
  const next = state.toolEvents.map((ev) => {
    if (!merged && ev.name === p.name && ev.summary === undefined) {
      merged = true;
      return { ...ev, summary: p.summary, chunks: p.chunks ?? [] };
    }
    return ev;
  });
  if (!merged) return state;
  return { ...state, toolEvents: next };
}

function applyDone(state: StreamingTurn, payload: unknown): StreamingTurn {
  // The orchestrator always sends `done` last, even after an `error`
  // frame. Preserve phase="error" so downstream consumers (e.g. the
  // AssistantTurn red block) treat phase as the single source of truth
  // for "did this turn fail?". `error` info on the turn stays
  // untouched either way; we just don't let phase silently roll back
  // to "done" once an error landed.
  const d = payload as {
    stop_reason?: string;
    message_id?: string;
    duration_ms?: number;
  };
  return {
    ...state,
    phase: state.phase === "error" ? "error" : "done",
    stopReason: d.stop_reason,
    messageID: d.message_id,
    completedAt: Date.now(),
    durationMs: typeof d.duration_ms === "number" ? d.duration_ms : state.durationMs,
  };
}

function applyFrame(state: StreamingTurn, frame: SSEFrame): StreamingTurn {
  // SSE payloads are JSON; if a frame fails to parse we drop it silently
  // rather than blowing up the whole turn — the next frame can still
  // make progress.
  let payload: unknown;
  try {
    payload = frame.data ? JSON.parse(frame.data) : {};
  } catch {
    return state;
  }

  switch (frame.event) {
    case "retrieving":
      return {
        ...state,
        phase: "retrieving",
        query: (payload as { query?: string }).query ?? state.query,
      };
    case "skipped_retrieval":
      // Rewriter decided retrieval is unnecessary (greeting, meta
      // question, history-only follow-up). Skip straight to streaming;
      // the evidence rail will hide for this turn.
      return {
        ...state,
        phase: "streaming",
        skippedRetrieval: true,
        query: (payload as { query?: string }).query ?? state.query,
        evidence: [],
      };
    case "evidence": {
      const chunks = (payload as { chunks?: ChunkPreview[] }).chunks ?? [];
      return { ...state, phase: "streaming", evidence: chunks };
    }
    case "text": {
      const delta = (payload as { delta?: string }).delta ?? "";
      return {
        ...state,
        phase: "streaming",
        answer: state.answer + delta,
      };
    }
    case "citation":
      return applyCitation(state, payload);
    case "usage":
      return { ...state, usage: payload as ChatUsage };
    case "title": {
      // Auto-title arrives between usage and done on the first
      // successful end_turn assistant message. chat-thread.tsx watches
      // the field and invalidates chatKeys.list() on done.
      const t = (payload as { title?: string }).title;
      if (!t) return state;
      return { ...state, autoTitle: t };
    }
    case "rewriter_status": {
      // Only emitted when the rewriter fell back. Reason values:
      // "timeout"|"empty"|"parse_failed"|"error". Stored on the turn
      // so PhaseChip can render the diagnostic glyph.
      const reason = (payload as { reason?: string }).reason ?? "error";
      return { ...state, rewriterFailureReason: reason };
    }
    case "title_status": {
      const reason = (payload as { reason?: string }).reason ?? "error";
      return { ...state, titleFailureReason: reason };
    }
    case "tool_start": {
      // One entry per dispatched tool call. The matching tool_result
      // frame lands later and merges its summary + chunks onto the
      // first event with this name that's still missing a summary.
      const p = payload as { name?: string; args?: string };
      if (!p.name) return state;
      return {
        ...state,
        toolEvents: [
          ...state.toolEvents,
          { name: p.name, args: p.args ?? "" },
        ],
      };
    }
    case "tool_result":
      return applyToolResult(state, payload);
    case "done":
      return applyDone(state, payload);
    case "error": {
      const message =
        (payload as { message?: string }).message ?? "unknown error";
      return {
        ...state,
        phase: "error",
        error: { kind: "protocol", message },
        completedAt: Date.now(),
      };
    }
    default:
      return state;
  }
}

export interface UseChatStreamResult {
  turn: StreamingTurn;
  start: (input: { content: string; model?: string }) => Promise<void>;
  cancel: () => void;
  reset: () => void;
}

/**
 * Caches a finished or in-flight StreamingTurn keyed per chat so
 * navigation away + back can hydrate the visible answer instead of
 * starting over. Exposed for tests; do not call directly from
 * components.
 */
export function streamCacheKey(chatID: string): readonly unknown[] {
  return chatKeys.stream(chatID);
}

/**
 * `streamFactory` is injected for testability — happy-dom's `fetch` doesn't
 * yield a real ReadableStream and stubbing the global is brittle.
 */
type StreamFactory = (
  chatID: string,
  body: { content: string; model?: string },
  signal: AbortSignal,
) => AsyncIterable<SSEFrame>;

const defaultFactory: StreamFactory = (chatID, body, signal) =>
  openChatMessageStream(chatID, body, signal);

export interface UseChatStreamOptions {
  /** Override the underlying stream source — used by Vitest. */
  streamFactory?: StreamFactory;
}

export function useChatStream(
  chatID: string | undefined,
  opts: UseChatStreamOptions = {},
): UseChatStreamResult {
  const factory = opts.streamFactory ?? defaultFactory;
  const queryClient = useQueryClient();

  const cachedKey = chatID ? streamCacheKey(chatID) : null;
  const cached = cachedKey
    ? queryClient.getQueryData<StreamingTurn>(cachedKey)
    : undefined;

  const [turn, dispatch] = useReducer(reducer, cached ?? INITIAL);

  const abortRef = useRef<AbortController | null>(null);
  const turnRef = useRef(turn);
  // Mirror reducer state into a ref for the unmount cleanup. Using a
  // layout effect for ref assignment satisfies the React Compiler's
  // refs-during-render rule.
  useEffect(() => {
    turnRef.current = turn;
  });

  // Persist final/in-flight state into the cache so the next mount of
  // this chat hydrates from it. The reducer ref lets us read the latest
  // state from the unmount cleanup without re-running the effect on
  // every state change.
  const cacheKeyRef = useRef(cachedKey);
  useEffect(() => {
    cacheKeyRef.current = cachedKey;
  });
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      const key = cacheKeyRef.current;
      if (!key) return;
      const t = turnRef.current;
      if (t.phase === "idle") return;
      queryClient.setQueryData(key, t);
    };
  }, [queryClient]);

  const start = useCallback<UseChatStreamResult["start"]>(
    async (input) => {
      if (!chatID) return;
      // If a stream is already running, cancel it before starting a new one.
      abortRef.current?.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;

      dispatch({ type: "start", userContent: input.content });

      try {
        const stream = factory(
          chatID,
          { content: input.content, model: input.model },
          ctrl.signal,
        );
        for await (const frame of stream) {
          if (ctrl.signal.aborted) break;
          dispatch({ type: "frame", frame });
        }
      } catch (err) {
        if (ctrl.signal.aborted) {
          // User-initiated cancel. The BE persists a partial assistant
          // message marked cancelled; the FE just surfaces "done with
          // partial answer" — no error frame needed.
          return;
        }
        const msg = err instanceof Error ? err.message : "stream failed";
        dispatch({ type: "transport_error", message: msg });
      } finally {
        if (abortRef.current === ctrl) {
          abortRef.current = null;
        }
      }
    },
    [chatID, factory],
  );

  const cancel = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const reset = useCallback(() => {
    abortRef.current?.abort();
    dispatch({ type: "reset" });
    if (cachedKey) queryClient.removeQueries({ queryKey: cachedKey });
  }, [cachedKey, queryClient]);

  return { turn, start, cancel, reset };
}
