import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI, setToken } from "@/lib/api-client";
import type { AuthResponse, User } from "@/lib/api-types";
import { authKeys, userKeys } from "@/lib/query-keys";

// Server returns the raw array under {data: []} of User entries.
// AdminUserRow is now identical to User (created_at is included for every
// user response since Phase 5 wired Me() through the DB), but we keep the
// alias because every admin call site already imports it.
export type AdminUserRow = User;

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

  // Self-rotation contract: when the user changes their own password,
  // the backend bumps the `token_version` row column and the caller's
  // existing JWT is now revoked (next request would 401 → /login).
  // To preserve the "rotate freely, stay signed in" UX, the backend
  // returns 200 with a freshly minted token. Swap it into localStorage
  // and update the cached `me` so subsequent requests authenticate.
  // For admin-changes-someone-else, the response is 204 (no body) —
  // the target user's sessions are deliberately revoked.
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
      if (res.status === 200) {
        const body = (await res.json()) as { data: AuthResponse };
        if (body.data?.token) {
          setToken(body.data.token);
          qc.setQueryData(authKeys.me(), body.data.user);
        }
        return;
      }
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
