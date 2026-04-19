import { QueryClient } from "@tanstack/react-query";
import { clearToken } from "./api-client";

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60 * 1000,
      retry: (failureCount, error) => {
        if (error instanceof Error && error.message === "Unauthorized")
          return false;
        return failureCount < 3;
      },
    },
    mutations: {
      onError: (error) => {
        if (error instanceof Error && error.message === "Unauthorized") {
          clearToken();
          queryClient.setQueryData(["auth", "me"], null);
        }
      },
    },
  },
});
