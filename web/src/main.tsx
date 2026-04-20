import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { ThemeProvider } from "@/components/theme-provider";
import { ResponsiveToaster } from "@/components/responsive-toaster";
import { queryClient } from "@/lib/query-client";
import { setUnauthorizedHandler } from "@/lib/api-client";
import { router } from "./router";
import "./index.css";

// On 401, drop cached user data and bounce to the login page.
setUnauthorizedHandler(() => {
  queryClient.setQueryData(["auth", "me"], null);
  router.navigate({ to: "/login" });
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
        <RouterProvider router={router} />
        <ResponsiveToaster />
        {import.meta.env.DEV && (
          <>
            <ReactQueryDevtools buttonPosition="bottom-left" />
            <TanStackRouterDevtools position="bottom-right" router={router} />
          </>
        )}
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
);
