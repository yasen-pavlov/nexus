import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import type { ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { useRelated } from "../use-related";

function wrap() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  return { Wrapper };
}

beforeEach(() => {
  setToken("fake-test-token");
});
afterEach(() => server.resetHandlers());

describe("useRelated", () => {
  it("returns related payload when enabled", async () => {
    server.use(
      http.get("*/api/documents/doc-1/related", () =>
        HttpResponse.json({
          data: { relations: [{ id: "x", type: "reply-to" }] },
        }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRelated("doc-1", true), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual({
      relations: [{ id: "x", type: "reply-to" }],
    });
  });

  it("does not fire when disabled", () => {
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRelated("doc-1", false), {
      wrapper: Wrapper,
    });
    expect(result.current.fetchStatus).toBe("idle");
  });
});
