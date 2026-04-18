import { describe, it, expect } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";
import { useIdentities } from "@/hooks/use-identities";

function wrap() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

describe("useIdentities", () => {
  it("builds bySourceType and byConnectorId maps from the response", async () => {
    const { result } = renderHook(() => useIdentities(), { wrapper: wrap() });

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.bySourceType.has("telegram")).toBe(true);
    const tg = result.current.bySourceType.get("telegram");
    expect(tg?.external_id).toBe("9001");
    expect(result.current.byConnectorId.has(tg!.connector_id)).toBe(true);
  });
});
