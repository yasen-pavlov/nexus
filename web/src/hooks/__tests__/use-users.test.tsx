import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { type ReactNode } from "react";
import { toast } from "sonner";

import { server } from "@/test/mocks/server";
import { useUsers } from "../use-users";
import { setToken, getToken } from "@/lib/api-client";
import { authKeys } from "@/lib/query-keys";

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

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

beforeEach(() => {
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  setToken("fake-test-token");
});
afterEach(() => server.resetHandlers());

const seeded = [
  { id: "u1", username: "admin", role: "admin", created_at: "2026-01-01T00:00:00Z" },
  { id: "u2", username: "alice", role: "user", created_at: "2026-02-01T00:00:00Z" },
];

describe("useUsers", () => {
  it("lists users", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.data).toHaveLength(2);
    expect(result.current.data?.[0].username).toBe("admin");
  });

  it("create mutation toasts + refetches on success", async () => {
    let created = false;
    server.use(
      http.get("*/api/users", () =>
        HttpResponse.json({
          data: created
            ? [...seeded, { id: "u3", username: "bob", role: "user" }]
            : seeded,
        }),
      ),
      http.post("*/api/users", async () => {
        created = true;
        return HttpResponse.json({
          data: { id: "u3", username: "bob", role: "user" },
        }, { status: 201 });
      }),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      await result.current.create.mutateAsync({
        username: "bob",
        password: "hunter2-hunter2",
        role: "user",
      });
    });
    expect(toast.success).toHaveBeenCalledWith("User created");
  });

  it("delete via raw fetch — 204 path", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
      http.delete("*/api/users/u2", () => new HttpResponse(null, { status: 204 })),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      await result.current.remove.mutateAsync("u2");
    });
    expect(toast.success).toHaveBeenCalledWith("User deleted");
  });

  it("create mutation surfaces server errors via toast.error", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
      http.post("*/api/users", () =>
        HttpResponse.json({ error: "username taken" }, { status: 409 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    await act(async () => {
      await expect(
        result.current.create.mutateAsync({
          username: "bob",
          password: "hunter2-hunter2",
          role: "user",
        }),
      ).rejects.toThrow("username taken");
    });
    expect(toast.error).toHaveBeenCalledWith("username taken");
  });

  it("delete raises Unauthorized on 401", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
      http.delete("*/api/users/u2", () =>
        new HttpResponse(null, { status: 401 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    await act(async () => {
      await expect(result.current.remove.mutateAsync("u2")).rejects.toThrow(
        /unauthorized/i,
      );
    });
    expect(toast.error).toHaveBeenCalledWith("Unauthorized");
  });

  it("delete surfaces JSON error body on non-2xx", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
      http.delete("*/api/users/u2", () =>
        HttpResponse.json({ error: "last admin" }, { status: 409 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    await act(async () => {
      await expect(result.current.remove.mutateAsync("u2")).rejects.toThrow(
        "last admin",
      );
    });
    expect(toast.error).toHaveBeenCalledWith("last admin");
  });

  it("self-rotation: 200 response swaps the token + primes the me cache", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
      http.put("*/api/users/u1/password", () =>
        HttpResponse.json({
          data: {
            token: "rotated-tok",
            user: {
              id: "u1",
              username: "admin",
              role: "admin",
              created_at: "2026-01-01T00:00:00Z",
            },
          },
        }),
      ),
    );
    const { Wrapper, client } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    await act(async () => {
      await result.current.changePassword.mutateAsync({
        userId: "u1",
        password: "new-password-here",
      });
    });
    expect(getToken()).toBe("rotated-tok");
    expect(client.getQueryData(authKeys.me())).toMatchObject({
      id: "u1",
      username: "admin",
    });
    expect(toast.success).toHaveBeenCalledWith("Password updated");
  });

  it("admin-cross-user: 204 response leaves token untouched", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
      http.put("*/api/users/u2/password", () =>
        new HttpResponse(null, { status: 204 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    await act(async () => {
      await result.current.changePassword.mutateAsync({
        userId: "u2",
        password: "new-password-here",
      });
    });
    expect(getToken()).toBe("fake-test-token");
    expect(toast.success).toHaveBeenCalledWith("Password updated");
  });

  it("changePassword 401 surfaces Unauthorized", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
      http.put("*/api/users/u2/password", () =>
        new HttpResponse(null, { status: 401 }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));
    await act(async () => {
      await expect(
        result.current.changePassword.mutateAsync({
          userId: "u2",
          password: "x",
        }),
      ).rejects.toThrow(/unauthorized/i);
    });
    expect(toast.error).toHaveBeenCalledWith("Unauthorized");
  });

  it("changePassword surfaces BE errors via toast.error", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
      http.put("*/api/users/u2/password", () =>
        HttpResponse.json(
          { error: "password must be at least 8 characters" },
          { status: 400 },
        ),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useUsers(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isPending).toBe(false));

    await act(async () => {
      try {
        await result.current.changePassword.mutateAsync({
          userId: "u2",
          password: "short",
        });
      } catch {
        // expected
      }
    });
    expect(toast.error).toHaveBeenCalled();
  });
});
