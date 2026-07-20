import { beforeEach, describe, expect, it } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setToken } from "@/lib/api-client";
import type { SSEFrame } from "@/lib/api-client";
import { useChatStream } from "../use-chat-stream";

function wrap() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  function Wrapper({ children }: Readonly<{ children: ReactNode }>) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  return { Wrapper, client };
}

function scriptedFactory(frames: SSEFrame[], opts: { errorMid?: boolean } = {}) {
  return async function* (): AsyncGenerator<SSEFrame, void, void> {
    for (const f of frames) {
      yield f;
    }
    if (opts.errorMid) {
      throw new Error("transport boom");
    }
  };
}

// abortableFactory yields `prefix` frames, then hangs until the AbortSignal
// fires and throws an AbortError — mimicking a real fetch stream that's
// cancelled mid-answer. Lets a test drive the stop / navigate-away paths.
function abortableFactory(prefix: SSEFrame[]) {
  return function (
    _chatID: string,
    _body: { content: string; model?: string },
    signal: AbortSignal,
  ): AsyncGenerator<SSEFrame, void, void> {
    return (async function* () {
      for (const f of prefix) {
        yield f;
      }
      await new Promise<void>((resolve) => {
        if (signal.aborted) {
          resolve();
          return;
        }
        signal.addEventListener("abort", () => resolve(), { once: true });
      });
      throw new DOMException("Aborted", "AbortError");
    })();
  };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

beforeEach(() => {
  setToken("tok");
});

describe("useChatStream", () => {
  it("starts in idle phase", () => {
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: scriptedFactory([]) }), {
      wrapper: Wrapper,
    });
    expect(result.current.turn.phase).toBe("idle");
    expect(result.current.turn.answer).toBe("");
    expect(result.current.turn.evidence).toEqual([]);
  });

  it("folds retrieving → evidence → text → done", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "retrieving", data: JSON.stringify({ query: "hello" }) },
      {
        event: "evidence",
        data: JSON.stringify({ chunks: [{ id: "d1", title: "Doc 1", source: "imap" }] }),
      },
      { event: "text", data: JSON.stringify({ delta: "Hello " }) },
      { event: "text", data: JSON.stringify({ delta: "world." }) },
      {
        event: "usage",
        data: JSON.stringify({ input: 100, output: 50, cache_read: 0, cache_write: 0 }),
      },
      {
        event: "done",
        data: JSON.stringify({ stop_reason: "end_turn", message_id: "msg1" }),
      },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });

    await act(async () => {
      await result.current.start({ content: "Hi", model: "anthropic:claude-sonnet-4-6" });
    });

    expect(result.current.turn.phase).toBe("done");
    expect(result.current.turn.answer).toBe("Hello world.");
    expect(result.current.turn.query).toBe("hello");
    expect(result.current.turn.evidence).toHaveLength(1);
    expect(result.current.turn.evidence[0].id).toBe("d1");
    expect(result.current.turn.usage?.input).toBe(100);
    expect(result.current.turn.stopReason).toBe("end_turn");
    expect(result.current.turn.messageID).toBe("msg1");
  });

  it("captures citations into the citations array", async () => {
    const factory = scriptedFactory([
      { event: "text", data: JSON.stringify({ delta: "Hello" }) },
      {
        event: "citation",
        data: JSON.stringify({ doc_id: "d1", cited_text: "h", span: [0, 5] }),
      },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: wrap().Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.citations).toHaveLength(1);
    expect(result.current.turn.citations[0]).toMatchObject({
      doc_id: "d1",
      span_start: 0,
      span_end: 5,
    });
  });

  it("handles a protocol error frame", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "error", data: JSON.stringify({ message: "model down" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "error", message_id: "x" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.error?.kind).toBe("protocol");
    expect(result.current.turn.error?.message).toBe("model down");
  });

  it("classifies a transport throw as transport error", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory(
      [{ event: "text", data: JSON.stringify({ delta: "partial" }) }],
      { errorMid: true },
    );
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.phase).toBe("error");
    expect(result.current.turn.error?.kind).toBe("transport");
    expect(result.current.turn.error?.message).toBe("transport boom");
    expect(result.current.turn.answer).toBe("partial");
  });

  it("ignores citation frames missing required fields", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "citation", data: JSON.stringify({ doc_id: "d1" }) }, // no span
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.citations).toHaveLength(0);
  });

  it("drops unparseable frame data without crashing", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "text", data: "not-json" },
      { event: "text", data: JSON.stringify({ delta: "ok" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.answer).toBe("ok");
  });

  it("reset clears state", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "text", data: JSON.stringify({ delta: "abc" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.answer).toBe("abc");
    act(() => result.current.reset());
    expect(result.current.turn.phase).toBe("idle");
    expect(result.current.turn.answer).toBe("");
  });

  it("noop when chatID is undefined", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "text", data: JSON.stringify({ delta: "ignored" }) },
    ]);
    const { result } = renderHook(() => useChatStream(undefined, { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.phase).toBe("idle");
  });

  it("ignores unknown event names", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "weird", data: "{}" },
      { event: "text", data: JSON.stringify({ delta: "fine" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.answer).toBe("fine");
  });

  it("hydrates from cache on remount", async () => {
    const { Wrapper, client } = wrap();
    const factory = scriptedFactory([
      { event: "text", data: JSON.stringify({ delta: "stored" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const first = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await first.result.current.start({ content: "x" });
    });
    first.unmount();

    // The cleanup effect should have written the state into the cache.
    // We render a fresh hook in the same QueryClient and expect the state.
    function HydratedWrapper({ children }: Readonly<{ children: ReactNode }>) {
      return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    }
    const second = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: HydratedWrapper,
    });
    await waitFor(() => expect(second.result.current.turn.answer).toBe("stored"));
  });

  // --- Phase 4: skipped_retrieval and title frames ---

  it("captures skipped_retrieval into the turn state and clears evidence", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      // Note: no retrieving / evidence frames on a skipped turn — the
      // BE goes straight from skipped_retrieval to the answer stream.
      {
        event: "skipped_retrieval",
        data: JSON.stringify({ query: "thanks" }),
      },
      { event: "text", data: JSON.stringify({ delta: "you're welcome" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "thanks!" });
    });
    expect(result.current.turn.skippedRetrieval).toBe(true);
    expect(result.current.turn.query).toBe("thanks");
    expect(result.current.turn.evidence).toEqual([]);
    expect(result.current.turn.phase).toBe("done");
    expect(result.current.turn.answer).toBe("you're welcome");
  });

  it("captures auto-title from the title frame and orders before done", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "retrieving", data: JSON.stringify({ query: "find invoices" }) },
      {
        event: "evidence",
        data: JSON.stringify({ chunks: [] }),
      },
      { event: "text", data: JSON.stringify({ delta: "here" }) },
      {
        event: "title",
        data: JSON.stringify({ title: "Anthropic invoice summary" }),
      },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "find invoices" });
    });
    expect(result.current.turn.autoTitle).toBe("Anthropic invoice summary");
    expect(result.current.turn.phase).toBe("done");
  });

  it("captures rewriter_status fallback reason on the turn", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      // Rewriter ran but timed out — orchestrator emits this BEFORE
      // retrieving so the FE knows the search was issued with the
      // user's literal phrasing.
      { event: "rewriter_status", data: JSON.stringify({ reason: "timeout" }) },
      { event: "retrieving", data: JSON.stringify({ query: "literal text" }) },
      { event: "evidence", data: JSON.stringify({ chunks: [] }) },
      { event: "text", data: JSON.stringify({ delta: "answer" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "literal text" });
    });
    expect(result.current.turn.rewriterFailureReason).toBe("timeout");
  });

  it("captures title_status failure reason", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "text", data: JSON.stringify({ delta: "ok" }) },
      { event: "title_status", data: JSON.stringify({ reason: "empty" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "hi" });
    });
    expect(result.current.turn.titleFailureReason).toBe("empty");
  });

  it("records startedAt on `start` so PhaseChip can render an elapsed counter", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    const before = Date.now();
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    const after = Date.now();
    const ts = result.current.turn.startedAt;
    expect(ts).toBeDefined();
    expect(ts!).toBeGreaterThanOrEqual(before);
    expect(ts!).toBeLessThanOrEqual(after);
  });

  it("ignores empty title frames", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "title", data: JSON.stringify({ title: "" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.autoTitle).toBeUndefined();
  });

  it("captures tool_start + tool_result into toolEvents", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "retrieving", data: JSON.stringify({ query: "x" }) },
      {
        event: "tool_start",
        data: JSON.stringify({ name: "nexus_search", args: '{"query":"y"}' }),
      },
      {
        event: "tool_result",
        data: JSON.stringify({
          name: "nexus_search",
          summary: "Searched \"y\" — 2 results",
          chunks: [
            { id: "c1", title: "Hit 1", source: "imap" },
            { id: "c2", title: "Hit 2", source: "imap" },
          ],
        }),
      },
      { event: "text", data: JSON.stringify({ delta: "answer" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "find y" });
    });
    expect(result.current.turn.toolEvents).toHaveLength(1);
    const ev = result.current.turn.toolEvents[0];
    expect(ev.name).toBe("nexus_search");
    expect(ev.args).toBe('{"query":"y"}');
    expect(ev.summary).toBe("Searched \"y\" — 2 results");
    expect(ev.chunks).toHaveLength(2);
  });

  it("folds tool_result chunks into the live evidence union (deduped, after initial)", async () => {
    // Regression: the one-shot `evidence` frame carries only the INITIAL
    // retrieval. Tool-fetched docs must also land in turn.evidence so the
    // streaming Sources footer can resolve citations to them — otherwise
    // the footer stays empty until the next turn ("sources lag one turn").
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      {
        event: "evidence",
        data: JSON.stringify({
          chunks: [{ id: "init1", title: "Initial", source: "telegram" }],
        }),
      },
      {
        event: "tool_start",
        data: JSON.stringify({ name: "nexus_search", args: "{}" }),
      },
      {
        event: "tool_result",
        data: JSON.stringify({
          name: "nexus_search",
          summary: "Searched — 2 results",
          chunks: [
            // init1 is a dup of the initial-retrieval chunk — must not double.
            { id: "init1", title: "Initial", source: "telegram" },
            { id: "tool1", title: "Tool-fetched", source: "paperless" },
          ],
        }),
      },
      { event: "text", data: JSON.stringify({ delta: "answer" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    // init1 (initial) then tool1 (tool-fetched, appended at tail), deduped.
    expect(result.current.turn.evidence.map((c) => c.id)).toEqual([
      "init1",
      "tool1",
    ]);
    // toolEvents still capture the raw per-call chunks unchanged.
    expect(result.current.turn.toolEvents[0].chunks).toHaveLength(2);
  });

  it("matches multiple tool_start/tool_result pairs in order", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "tool_start", data: JSON.stringify({ name: "nexus_search", args: "{}" }) },
      {
        event: "tool_result",
        data: JSON.stringify({ name: "nexus_search", summary: "first", chunks: [] }),
      },
      { event: "tool_start", data: JSON.stringify({ name: "nexus_search", args: "{}" }) },
      {
        event: "tool_result",
        data: JSON.stringify({ name: "nexus_search", summary: "second", chunks: [] }),
      },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.toolEvents).toHaveLength(2);
    expect(result.current.turn.toolEvents[0].summary).toBe("first");
    expect(result.current.turn.toolEvents[1].summary).toBe("second");
  });

  it("preserves phase=error when `done` follows an `error` frame", async () => {
    // Orchestrator pattern: persistAndDone emits EvError then EvDone
    // on every error path. The reducer must keep phase="error" so the
    // AssistantTurn red block fires; the prior bug let phase silently
    // roll back to "done" while the error info stayed on the turn.
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      { event: "text", data: JSON.stringify({ delta: "partial " }) },
      { event: "error", data: JSON.stringify({ message: "model boom" }) },
      { event: "done", data: JSON.stringify({ stop_reason: "error", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.phase).toBe("error");
    expect(result.current.turn.error?.kind).toBe("protocol");
    expect(result.current.turn.error?.message).toBe("model boom");
    expect(result.current.turn.answer).toBe("partial ");
    // done-frame metadata still lands so the message persists / refresh works.
    expect(result.current.turn.messageID).toBe("m");
    expect(result.current.turn.stopReason).toBe("error");
  });

  it("cancel() settles a streaming turn to a terminal phase (composer re-enables)", async () => {
    // Regression: stopping mid-stream used to leave phase stuck at
    // "streaming" forever — isStreaming stayed true, so the composer was
    // permanently disabled. The turn must land in a terminal phase with the
    // partial answer preserved and no messageID (so the done-refetch stays
    // inert).
    const { Wrapper } = wrap();
    const factory = abortableFactory([
      { event: "retrieving", data: JSON.stringify({ query: "q" }) },
      { event: "text", data: JSON.stringify({ delta: "partial" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });

    let started: Promise<void> = Promise.resolve();
    await act(async () => {
      started = result.current.start({ content: "x" });
      await tick();
    });
    expect(result.current.turn.phase).toBe("streaming");
    expect(result.current.turn.answer).toBe("partial");

    await act(async () => {
      result.current.cancel();
      await started;
    });

    expect(result.current.turn.phase).toBe("done");
    expect(result.current.turn.answer).toBe("partial");
    expect(result.current.turn.messageID).toBeUndefined();
  });

  it("navigating away mid-stream caches a settled turn, not a frozen streaming one", async () => {
    // Regression: unmounting mid-stream persisted the in-flight turn as
    // phase="streaming", so returning to the chat re-hydrated a frozen card
    // with a disabled composer until a full page reload.
    const { Wrapper, client } = wrap();
    const factory = abortableFactory([
      { event: "text", data: JSON.stringify({ delta: "half-written" }) },
    ]);
    const first = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });

    let started: Promise<void> = Promise.resolve();
    await act(async () => {
      started = first.result.current.start({ content: "x" });
      await tick();
    });
    // Wait for the streamed text to actually land in the turn BEFORE navigating
    // away. Otherwise the unmount below can cache a turn whose answer is still
    // "" (the text event hadn't propagated to state yet), and the re-hydrated
    // second hook never shows "half-written" — a race that surfaces under
    // coverage's slower timing.
    await waitFor(() =>
      expect(first.result.current.turn.answer).toBe("half-written"),
    );
    expect(first.result.current.turn.phase).toBe("streaming");

    // Navigate away: unmount aborts the stream and caches the turn.
    first.unmount();
    await act(async () => {
      await started;
    });

    function Rewrap({ children }: Readonly<{ children: ReactNode }>) {
      return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    }
    const second = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Rewrap,
    });
    await waitFor(() => expect(second.result.current.turn.answer).toBe("half-written"));
    expect(second.result.current.turn.phase).toBe("done");
  });

  it("drops a tool_result without a matching prior tool_start without crashing", async () => {
    const { Wrapper } = wrap();
    const factory = scriptedFactory([
      // Orphan tool_result — should be a no-op and not throw.
      {
        event: "tool_result",
        data: JSON.stringify({ name: "nexus_search", summary: "ghost", chunks: [] }),
      },
      { event: "done", data: JSON.stringify({ stop_reason: "end_turn", message_id: "m" }) },
    ]);
    const { result } = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start({ content: "x" });
    });
    expect(result.current.turn.toolEvents).toEqual([]);
    expect(result.current.turn.phase).toBe("done");
  });
});
