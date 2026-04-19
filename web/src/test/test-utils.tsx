import { render, type RenderOptions } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createRouter,
  createRootRoute,
  createRoute,
  createMemoryHistory,
} from "@tanstack/react-router";

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
}

/**
 * Render a component wrapped in QueryClientProvider only (no router).
 * Use renderWithRouter when the component needs TanStack Router context.
 */
function renderWithProviders(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  const queryClient = createTestQueryClient();
  return render(
    <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>,
    options,
  );
}

/**
 * Render within a minimal TanStack Router so hooks like useNavigate work.
 * The component under test is rendered at the "/" route. Pass `extraRoutes`
 * for components that use `<Link to="/...">` beyond the index — without
 * registration Links render without hrefs and assertions that rely on
 * nav fail silently.
 */
function renderWithRouter(
  ui: React.ReactElement,
  options?: { initialPath?: string; extraRoutes?: string[] },
) {
  const queryClient = createTestQueryClient();

  const rootRoute = createRootRoute({
    component: () => ui,
  });

  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => null,
  });

  const childRoutes = [
    indexRoute,
    ...(options?.extraRoutes ?? []).map((path) =>
      createRoute({
        getParentRoute: () => rootRoute,
        path,
        component: () => null,
      }),
    ),
  ];

  const router = createRouter({
    routeTree: rootRoute.addChildren(childRoutes),
    history: createMemoryHistory({
      initialEntries: [options?.initialPath ?? "/"],
    }),
    context: { queryClient },
  });

  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

export { renderWithProviders as render, renderWithRouter, createTestQueryClient };
export { screen, waitFor, within, act } from "@testing-library/react";
export { default as userEvent } from "@testing-library/user-event";
