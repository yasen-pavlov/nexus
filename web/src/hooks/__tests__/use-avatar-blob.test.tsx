import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import type { ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { useAvatarBlob } from "../use-avatar-blob";

function wrap() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity, staleTime: Infinity },
    },
  });
  function Wrapper({ children }: Readonly<{ children: ReactNode }>) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  return { Wrapper, client };
}

beforeEach(() => {
  setToken("tok");
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useAvatarBlob", () => {
  it("caches the Blob and re-mints a live URL on remount (no dead blob: URL)", async () => {
    // Regression: the hook used to cache the object URL and revoke it on
    // unmount, so a remount within the cache window returned a revoked URL
    // and the <img> broke. Now it caches the Blob and mints a per-consumer
    // URL, so revisiting re-mints a live one and the fetch runs only once.
    let fetches = 0;
    server.use(
      http.get("*/avatars/*", () => {
        fetches += 1;
        return new HttpResponse(new Blob(["img-bytes"]), { status: 200 });
      }),
    );

    const minted: string[] = [];
    const revoked: string[] = [];
    let n = 0;
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => {
      const u = `blob:mock-${n++}`;
      minted.push(u);
      return u;
    });
    vi.spyOn(URL, "revokeObjectURL").mockImplementation((u: string) => {
      revoked.push(u);
    });

    const { Wrapper, client } = wrap();
    const first = renderHook(() => useAvatarBlob("conn-1", "ext-1"), { wrapper: Wrapper });
    await waitFor(() => expect(first.result.current.data).toBe("blob:mock-0"));
    expect(fetches).toBe(1);

    // Navigate away: the consumer's URL is revoked.
    first.unmount();
    expect(revoked).toContain("blob:mock-0");

    // Revisit within the cache window: a fresh URL is minted from the still
    // cached Blob, the fetch does NOT run again, and the URL isn't revoked.
    function Rewrap({ children }: Readonly<{ children: ReactNode }>) {
      return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    }
    const second = renderHook(() => useAvatarBlob("conn-1", "ext-1"), { wrapper: Rewrap });
    await waitFor(() => expect(second.result.current.data).toBe("blob:mock-1"));
    expect(fetches).toBe(1);
    expect(revoked).not.toContain("blob:mock-1");
  });
});
