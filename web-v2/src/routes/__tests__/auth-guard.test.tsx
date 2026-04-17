import { describe, it, expect } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { routeTree } from "@/routeTree.gen";
import { setToken } from "@/lib/api-client";
import { fakeToken, fakeUserToken } from "@/test/mocks/handlers";

function setup(initialPath: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

describe("auth guards", () => {
  it("unauthenticated user is redirected from / to /login", async () => {
    const router = setup("/");
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/login");
    });
  });

  it("unauthenticated user is redirected from /admin/settings to /login", async () => {
    const router = setup("/admin/settings");
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/login");
    });
  });

  it("authenticated user visiting /login is redirected to /", async () => {
    setToken(fakeToken);
    const router = setup("/login");
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/");
    });
  });

  it("non-admin user visiting /admin/settings is redirected to /", async () => {
    setToken(fakeUserToken);
    const router = setup("/admin/settings");
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/");
    });
  });

  it("admin user can access /admin/settings", async () => {
    setToken(fakeToken);
    const router = setup("/admin/settings");
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/admin/settings");
    });
  });
});
