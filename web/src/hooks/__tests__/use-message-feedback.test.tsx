import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { type ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { useMessageFeedback } from "../use-message-feedback";
import { setToken } from "@/lib/api-client";

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
  return { Wrapper };
}

beforeEach(() => setToken("fake-test-token"));
afterEach(() => server.resetHandlers());

describe("useMessageFeedback", () => {
  it("PUTs the feedback to the message endpoint", async () => {
    let captured: { url: string; body: unknown } | undefined;
    server.use(
      http.put("*/api/chats/:chatId/messages/:messageId/feedback", async ({ request }) => {
        captured = { url: request.url, body: await request.json() };
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useMessageFeedback(), { wrapper: Wrapper });

    await act(async () => {
      await result.current.mutateAsync({
        chatId: "chat-1",
        messageId: "msg-1",
        feedback: "up",
      });
    });

    await waitFor(() => expect(captured).toBeDefined());
    expect(captured!.url).toContain("/api/chats/chat-1/messages/msg-1/feedback");
    expect(captured!.body).toEqual({ feedback: "up" });
  });

  it("sends feedback:null to clear a rating", async () => {
    let body: unknown;
    server.use(
      http.put("*/api/chats/:chatId/messages/:messageId/feedback", async ({ request }) => {
        body = await request.json();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useMessageFeedback(), { wrapper: Wrapper });
    await act(async () => {
      await result.current.mutateAsync({ chatId: "c", messageId: "m", feedback: null });
    });
    expect(body).toEqual({ feedback: null });
  });
});
