import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type { User } from "@/lib/api-types";
import { userKeys } from "@/lib/query-keys";

// Server returns the raw array under {data: []}, but its entries are User-
// shaped without created_at because the auth response struct doesn't include
// it. The Users page needs created_at, so we expose it via a curated fetch
// that accepts the optional field.
export interface AdminUserRow extends User {
  created_at?: string;
}

export interface CreateUserArgs {
  username: string;
  password: string;
  role: "admin" | "user";
}

export interface ChangePasswordArgs {
  userId: string;
  password: string;
}

export function useUsers() {
  const qc = useQueryClient();

  const query = useQuery<AdminUserRow[]>({
    queryKey: userKeys.list(),
    queryFn: () => fetchAPI<AdminUserRow[]>("/api/users"),
    staleTime: 30_000,
  });

  const create = useMutation({
    mutationFn: (args: CreateUserArgs) =>
      fetchAPI<AdminUserRow>("/api/users", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(args),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: userKeys.list() });
      toast.success("User created");
    },
    onError: (err: Error) => toast.error(err.message || "Create failed"),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => {
      const token = localStorage.getItem("nexus_jwt");
      const res = await fetch(`/api/users/${id}`, {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (res.status === 401) {
        throw new Error("Unauthorized");
      }
      if (!res.ok && res.status !== 204) {
        const body = await res.json().catch(() => ({ error: "" }));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: userKeys.list() });
      toast.success("User deleted");
    },
    onError: (err: Error) => toast.error(err.message || "Delete failed"),
  });

  const changePassword = useMutation({
    mutationFn: async ({ userId, password }: ChangePasswordArgs) => {
      const token = localStorage.getItem("nexus_jwt");
      const res = await fetch(`/api/users/${userId}/password`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ password }),
      });
      if (res.status === 401) throw new Error("Unauthorized");
      if (!res.ok && res.status !== 204) {
        const body = await res.json().catch(() => ({ error: "" }));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
    },
    onSuccess: () => toast.success("Password updated"),
    onError: (err: Error) => toast.error(err.message || "Change password failed"),
  });

  return { ...query, create, remove, changePassword };
}
