import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { renderHook, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import type { ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { setToken, getToken } from "@/lib/api-client";
import { useDocumentDownload } from "../use-document-download";

function wrap() {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
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

describe("useDocumentDownload", () => {
  it("uses Content-Disposition filename and triggers a click", async () => {
    server.use(
      http.get("*/api/documents/doc-1/content", () =>
        HttpResponse.text("hello", {
          headers: { "Content-Disposition": 'attachment; filename="note.txt"' },
        }),
      ),
    );

    const clickSpy = vi.fn();
    const origCreate = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tag: string) => {
      const el = origCreate(tag);
      if (tag === "a") el.click = clickSpy;
      return el;
    });

    const { Wrapper } = wrap();
    const { result } = renderHook(() => useDocumentDownload(), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.mutateAsync({ id: "doc-1" });
    });
    expect(clickSpy).toHaveBeenCalled();
  });

  it("clears token on 401", async () => {
    server.use(
      http.get("*/api/documents/doc-1/content", () =>
        new HttpResponse(null, { status: 401 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useDocumentDownload(), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await expect(
        result.current.mutateAsync({ id: "doc-1" }),
      ).rejects.toThrow(/unauthorized/i);
    });
    expect(getToken()).toBeNull();
  });

  it("surfaces JSON error body on non-2xx", async () => {
    server.use(
      http.get("*/api/documents/doc-1/content", () =>
        HttpResponse.json({ error: "doc missing" }, { status: 404 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useDocumentDownload(), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await expect(
        result.current.mutateAsync({ id: "doc-1" }),
      ).rejects.toThrow("doc missing");
    });
  });

  it("falls back to suggestedFilename when header is absent", async () => {
    server.use(
      http.get("*/api/documents/doc-1/content", () =>
        HttpResponse.text("hello"),
      ),
    );
    const anchor = document.createElement("a");
    anchor.click = vi.fn();
    vi.spyOn(document, "createElement").mockReturnValue(anchor);
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useDocumentDownload(), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.mutateAsync({
        id: "doc-1",
        suggestedFilename: "fallback.pdf",
      });
    });
    expect(anchor.download).toBe("fallback.pdf");
  });
});
