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
import { setToken } from "@/lib/api-client";
import { connectorKeys } from "@/lib/query-keys";
import { useConnectors, useConnector } from "../use-connectors";

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
  setToken("tok");
  navigate.mockClear();
});
afterEach(() => server.resetHandlers());

const baseConnector = {
  id: "c-1",
  type: "filesystem",
  name: "docs",
  enabled: true,
  schedule: "0 * * * *",
  shared: false,
  config: {},
};

describe("useConnectors", () => {
  it("lists, creates, updates and deletes connectors", async () => {
    let created = false;
    let updated = false;
    let deleted = false;
    server.use(
      http.get("*/api/connectors/", () =>
        HttpResponse.json({ data: [baseConnector] }),
      ),
      http.post("*/api/connectors/", () => {
        created = true;
        return HttpResponse.json({ data: { ...baseConnector, id: "c-2" } });
      }),
      http.put("*/api/connectors/c-1", () => {
        updated = true;
        return HttpResponse.json({ data: { ...baseConnector, name: "renamed" } });
      }),
      http.delete("*/api/connectors/c-1", () => {
        deleted = true;
        return HttpResponse.json({ data: null });
      }),
    );

    const { Wrapper } = wrap();
    const { result } = renderHook(() => useConnectors(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.connectors).toHaveLength(1);

    await act(async () => {
      await result.current.createConnector({
        type: "filesystem",
        name: "new",
        config: {},
        enabled: true,
        schedule: "",
        shared: false,
      });
    });
    expect(created).toBe(true);

    await act(async () => {
      await result.current.updateConnector({
        id: "c-1",
        type: "filesystem",
        name: "renamed",
        config: {},
        enabled: true,
        schedule: "",
        shared: false,
      });
    });
    expect(updated).toBe(true);

    await act(async () => {
      await result.current.deleteConnector("c-1");
    });
    expect(deleted).toBe(true);
  });
});

describe("useConnector", () => {
  it("loads detail + runs, updates, and deletes with navigate", async () => {
    server.use(
      http.get("*/api/connectors/c-1", () =>
        HttpResponse.json({ data: baseConnector }),
      ),
      http.get("*/api/connectors/c-1/runs", () =>
        HttpResponse.json({ data: [{ id: "r1" }] }),
      ),
      http.put("*/api/connectors/c-1", () =>
        HttpResponse.json({ data: { ...baseConnector, name: "renamed" } }),
      ),
      http.delete("*/api/connectors/c-1", () =>
        HttpResponse.json({ data: null }),
      ),
    );

    const { Wrapper, client } = wrap();
    client.setQueryData(connectorKeys.detail("c-1"), baseConnector);
    const { result } = renderHook(() => useConnector("c-1"), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.connector?.id).toBe("c-1"));
    await waitFor(() => expect(result.current.runs).toHaveLength(1));

    await act(async () => {
      await result.current.updateConnector({
        type: "filesystem",
        name: "renamed",
        config: {},
        enabled: true,
        schedule: "",
        shared: false,
      });
    });

    await act(async () => {
      await result.current.deleteConnector();
    });
    expect(navigate).toHaveBeenCalledWith({ to: "/connectors" });
  });

  it("stays idle when id is empty", () => {
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useConnector(""), { wrapper: Wrapper });
    expect(result.current.isLoading).toBe(true);
    expect(result.current.connector).toBeUndefined();
  });
});
