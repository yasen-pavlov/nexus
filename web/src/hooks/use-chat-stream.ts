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
  query?: string;
  evidence: ChunkPreview[];
  answer: string;
  citations: ChatCitation[];
  usage?: ChatUsage;
  stopReason?: string;
  messageID?: string;
  error?: StreamErrorInfo;
}

const INITIAL: StreamingTurn = {
  phase: "idle",
  userContent: "",
  evidence: [],
  answer: "",
  citations: [],
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
      };
    case "transport_error":
      return {
        ...state,
        phase: "error",
        error: { kind: "transport", message: action.message },
      };
    case "reset":
      return INITIAL;
    case "hydrate":
      return action.turn;
    case "frame":
      return applyFrame(state, action.frame);
  }
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
    case "citation": {
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
    case "usage":
      return { ...state, usage: payload as ChatUsage };
    case "done": {
      const d = payload as { stop_reason?: string; message_id?: string };
      return {
        ...state,
        phase: "done",
        stopReason: d.stop_reason,
        messageID: d.message_id,
      };
    }
    case "error": {
      const message =
        (payload as { message?: string }).message ?? "unknown error";
      return {
        ...state,
        phase: "error",
        error: { kind: "protocol", message },
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
