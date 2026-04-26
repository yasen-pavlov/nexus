import { beforeEach, describe, expect, it } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import type { ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";

import { useChats, useCreateChat, useDeleteChat } from "../use-chats";

const baseChat = {
  id: "c-1",
  user_id: "u-1",
  title: "",
  default_model: "anthropic:claude-sonnet-4-6",
  created_at: "2026-04-26T00:00:00Z",
  updated_at: "2026-04-26T00:00:00Z",
};

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

beforeEach(() => {
  setToken("tok");
});

describe("useChats", () => {
  it("returns the list response and total", async () => {
    server.use(
      http.get("*/api/chats", () =>
        HttpResponse.json({
          data: {
            chats: [{ ...baseChat, first_message_preview: "hi there" }],
            total: 1,
          },
        }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useChats(50, 0), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.chats).toHaveLength(1);
    expect(result.current.total).toBe(1);
    expect(result.current.chats[0].first_message_preview).toBe("hi there");
  });
});

describe("useCreateChat", () => {
  it("posts and returns the new chat", async () => {
    server.use(http.post("*/api/chats", () => HttpResponse.json({ data: baseChat })));
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useCreateChat(), { wrapper: Wrapper });
    let returned;
    await act(async () => {
      returned = await result.current.mutateAsync({ default_model: "anthropic:claude-sonnet-4-6" });
    });
    expect(returned).toMatchObject({ id: "c-1" });
  });
});

describe("useDeleteChat", () => {
  it("returns void on 204 No Content", async () => {
    server.use(
      http.delete("*/api/chats/c-1", () => new HttpResponse(null, { status: 204 })),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useDeleteChat(), { wrapper: Wrapper });
    await act(async () => {
      await result.current.mutateAsync("c-1");
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });
});
