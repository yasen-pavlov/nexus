import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";
import type { ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { useTelegramAuth } from "../use-telegram-auth";

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

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

beforeEach(() => {
  setToken("tok");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});
afterEach(() => server.resetHandlers());

describe("useTelegramAuth", () => {
  it("start moves phase idle → code-sent on success", async () => {
    server.use(
      http.post("*/api/connectors/c-1/auth/start", () =>
        HttpResponse.json({ data: { status: "ok" } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useTelegramAuth("c-1"), {
      wrapper: Wrapper,
    });
    expect(result.current.phase).toBe("idle");
    await act(async () => {
      await result.current.start();
    });
    expect(result.current.phase).toBe("code-sent");
    expect(toast.success).toHaveBeenCalled();
  });

  it("start failure lands phase=failed and surfaces the error", async () => {
    server.use(
      http.post("*/api/connectors/c-1/auth/start", () =>
        HttpResponse.json({ error: "flood_wait" }, { status: 429 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useTelegramAuth("c-1"), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start();
    });
    await waitFor(() => expect(result.current.phase).toBe("failed"));
    expect(result.current.error).toBe("flood_wait");
    expect(toast.error).toHaveBeenCalled();
  });

  it("submit success → phase=authenticated", async () => {
    server.use(
      http.post("*/api/connectors/c-1/auth/code", () =>
        HttpResponse.json({ data: { status: "ok" } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useTelegramAuth("c-1"), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.submit({ code: "12345" });
    });
    await waitFor(() => expect(result.current.phase).toBe("authenticated"));
  });

  it("submit failure that mentions 2FA sets needs2FA", async () => {
    // Use 400 — `fetchAPI` short-circuits 401 into the generic "Unauthorized"
    // error, which would never match the /2fa|password/i branch under test.
    server.use(
      http.post("*/api/connectors/c-1/auth/code", () =>
        HttpResponse.json({ error: "2FA password required" }, { status: 400 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useTelegramAuth("c-1"), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.submit({ code: "12345" });
    });
    await waitFor(() => expect(result.current.needs2FA).toBe(true));
    expect(result.current.phase).toBe("failed");
  });

  it("submit failure without 2FA text leaves needs2FA false", async () => {
    server.use(
      http.post("*/api/connectors/c-1/auth/code", () =>
        HttpResponse.json({ error: "PHONE_CODE_INVALID" }, { status: 400 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useTelegramAuth("c-1"), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.submit({ code: "12345" });
    });
    await waitFor(() => expect(result.current.phase).toBe("failed"));
    expect(result.current.needs2FA).toBe(false);
  });

  it("reset brings state back to idle", async () => {
    server.use(
      http.post("*/api/connectors/c-1/auth/start", () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useTelegramAuth("c-1"), {
      wrapper: Wrapper,
    });
    await act(async () => {
      await result.current.start();
    });
    await waitFor(() => expect(result.current.phase).toBe("failed"));
    act(() => result.current.reset());
    expect(result.current.phase).toBe("idle");
    expect(result.current.error).toBeUndefined();
    expect(result.current.needs2FA).toBe(false);
  });
});
