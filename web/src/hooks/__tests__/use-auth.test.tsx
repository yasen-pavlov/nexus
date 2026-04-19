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
import type { ReactNode } from "react";

import { server } from "@/test/mocks/server";
import { setToken, getToken } from "@/lib/api-client";
import { authKeys } from "@/lib/query-keys";
import {
  useMe,
  useHealth,
  useLogin,
  useRegister,
  useLogout,
} from "../use-auth";

const navigate = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
}));

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
  navigate.mockClear();
});
afterEach(() => server.resetHandlers());

describe("useMe", () => {
  it("stays disabled when no token is present", () => {
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useMe(), { wrapper: Wrapper });
    expect(result.current.fetchStatus).toBe("idle");
  });

  it("fetches when a token is present", async () => {
    setToken("tok");
    server.use(
      http.get("*/api/auth/me", () =>
        HttpResponse.json({ data: { id: "u1", username: "muty", role: "admin" } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useMe(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.username).toBe("muty");
  });
});

describe("useHealth", () => {
  it("returns the health payload", async () => {
    server.use(
      http.get("*/api/health", () =>
        HttpResponse.json({ data: { status: "ok", setup_required: false } }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useHealth(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.status).toBe("ok");
  });
});

describe("useLogin", () => {
  it("stores token and primes the me cache on success", async () => {
    server.use(
      http.post("*/api/auth/login", () =>
        HttpResponse.json({
          data: { token: "new-tok", user: { id: "u1", username: "muty", role: "admin" } },
        }),
      ),
    );
    const { Wrapper, client } = wrap();
    const { result } = renderHook(() => useLogin(), { wrapper: Wrapper });
    await act(async () => {
      await result.current.mutateAsync({ username: "muty", password: "x" });
    });
    expect(getToken()).toBe("new-tok");
    expect(client.getQueryData(authKeys.me())).toMatchObject({ username: "muty" });
  });
});

describe("useRegister", () => {
  it("stores token on register success", async () => {
    server.use(
      http.post("*/api/auth/register", () =>
        HttpResponse.json({
          data: { token: "reg-tok", user: { id: "u1", username: "muty", role: "admin" } },
        }),
      ),
    );
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRegister(), { wrapper: Wrapper });
    await act(async () => {
      await result.current.mutateAsync({ username: "muty", password: "x" });
    });
    await waitFor(() => expect(getToken()).toBe("reg-tok"));
  });
});

describe("useLogout", () => {
  it("clears the token, wipes the cache, and navigates to /login", () => {
    setToken("tok");
    const { Wrapper, client } = wrap();
    client.setQueryData(authKeys.me(), { id: "u1" });
    const { result } = renderHook(() => useLogout(), { wrapper: Wrapper });
    act(() => result.current());
    expect(getToken()).toBeNull();
    expect(client.getQueryData(authKeys.me())).toBeUndefined();
    expect(navigate).toHaveBeenCalledWith({ to: "/login" });
  });
});
