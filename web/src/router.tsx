import { createRouter } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";
import { queryClient } from "./lib/query-client";
import { ErrorPage } from "./components/error-page";

export const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
  defaultNotFoundComponent: () => <ErrorPage kind="404" />,
  // The router types thrown values as `unknown` (anything can be thrown);
  // ErrorPage renders Error.stack, so wrap non-Error throws.
  defaultErrorComponent: ({ error }) => (
    <ErrorPage
      kind="error"
      error={error instanceof Error ? error : new Error(String(error))}
    />
  ),
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
