import { describe, it, expect } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";
import { useConversation } from "@/hooks/use-conversation";

function wrap() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  return { Wrapper, client };
}

describe("useConversation", () => {
  it("loads messages and exposes them sorted chronologically", async () => {
    const { Wrapper } = wrap();
    const { result } = renderHook(
      () => useConversation("telegram", "12345"),
      { wrapper: Wrapper },
    );

    await waitFor(() => expect(result.current.isLoadingInitial).toBe(false));
    expect(result.current.messages.length).toBeGreaterThan(0);

    const timestamps = result.current.messages.map((m) =>
      new Date(m.created_at).getTime(),
    );
    const sorted = [...timestamps].sort((a, b) => a - b);
    expect(timestamps).toEqual(sorted);
  });

  it("dedupes messages by source_id", async () => {
    const { Wrapper } = wrap();
    const { result } = renderHook(
      () => useConversation("telegram", "12345"),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.isLoadingInitial).toBe(false));
    const ids = result.current.messages.map((m) => m.source_id);
    expect(new Set(ids).size).toBe(ids.length);
  });
});
