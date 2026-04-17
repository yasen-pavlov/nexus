import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { fetchAPI, setToken, clearToken, getToken } from "@/lib/api-client";
import type { User, AuthResponse, HealthResponse } from "@/lib/api-types";
import { authKeys } from "@/lib/query-keys";

export function useMe() {
  return useQuery({
    queryKey: authKeys.me(),
    queryFn: () => fetchAPI<User>("/api/auth/me"),
    enabled: !!getToken(),
    retry: false,
  });
}

export function useHealth() {
  return useQuery({
    queryKey: authKeys.health(),
    queryFn: () => fetchAPI<HealthResponse>("/api/health"),
  });
}

export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      username,
      password,
    }: {
      username: string;
      password: string;
    }) =>
      fetchAPI<AuthResponse>("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
      }),
    onSuccess: (data) => {
      setToken(data.token);
      queryClient.setQueryData(authKeys.me(), data.user);
    },
  });
}

export function useRegister() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      username,
      password,
    }: {
      username: string;
      password: string;
    }) =>
      fetchAPI<AuthResponse>("/api/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
      }),
    onSuccess: (data) => {
      setToken(data.token);
      queryClient.setQueryData(authKeys.me(), data.user);
      queryClient.setQueryData(authKeys.health(), {
        status: "ok",
        setup_required: false,
      });
    },
  });
}

export function useLogout() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  return () => {
    clearToken();
    queryClient.setQueryData(authKeys.me(), null);
    queryClient.clear();
    void navigate({ to: "/login" });
  };
}
