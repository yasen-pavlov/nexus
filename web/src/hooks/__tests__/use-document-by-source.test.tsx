import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import type { ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { useDocumentBySource } from "../use-document-by-source";

function wrap() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  return { Wrapper };
}

beforeEach(() => setToken("fake-test-token"));
afterEach(() => server.resetHandlers());

describe("useDocumentBySource", () => {
  it("encodes both params in the query string", async () => {
    let observedURL = "";
    server.use(
      http.get("*/api/documents/by-source", ({ request }) => {
        observedURL = request.url;
        return HttpResponse.json({
          data: { id: "doc-9", source_type: "telegram", source_id: "42:1-3" },
        });
      }),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(
      () => useDocumentBySource("telegram", "42:1-3"),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(observedURL).toContain("source_type=telegram");
    expect(observedURL).toContain("source_id=42%3A1-3");
    expect(result.current.data?.id).toBe("doc-9");
  });

  it("stays idle when sourceID is missing", () => {
    const { Wrapper } = wrap();
    const { result } = renderHook(
      () => useDocumentBySource("telegram", null),
      { wrapper: Wrapper },
    );
    expect(result.current.fetchStatus).toBe("idle");
  });

  it("stays idle when explicitly disabled", () => {
    const { Wrapper } = wrap();
    const { result } = renderHook(
      () => useDocumentBySource("telegram", "42:1-3", false),
      { wrapper: Wrapper },
    );
    expect(result.current.fetchStatus).toBe("idle");
  });
});
