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
  function Wrapper({ children }: { children: ReactNode }) {
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
    function HydratedWrapper({ children }: { children: ReactNode }) {
      return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    }
    const second = renderHook(() => useChatStream("c1", { streamFactory: factory }), {
      wrapper: HydratedWrapper,
    });
    await waitFor(() => expect(second.result.current.turn.answer).toBe("stored"));
  });
});
