import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI, getToken, handleUnauthorized, setToken } from "@/lib/api-client";
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

  // fetchAPI handles the 401 (clear token + redirect) and the 204 no-content
  // success path for us — no need to hand-roll the token header or error mapping.
  const remove = useMutation({
    mutationFn: (id: string) =>
      fetchAPI<void>(`/api/users/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: userKeys.list() });
      toast.success("User deleted");
    },
    onError: (err: Error) => toast.error(err.message || "Delete failed"),
  });

  const changePassword = useChangePassword();

  return { ...query, create, remove, changePassword };
}

/**
 * Standalone password-change mutation. Kept separate from useUsers() so
 * callers that only need to rotate a password (the account page's
 * ChangePasswordSheet) don't also mount the admin-only /api/users roster
 * query — which would 403 + retry for a regular user.
 *
 * Self-rotation contract: when the user changes their own password, the
 * backend bumps the `token_version` row column and the caller's existing JWT
 * is now revoked (next request would 401 → /login). To preserve the "rotate
 * freely, stay signed in" UX, the backend returns 200 with a freshly minted
 * token. Swap it into localStorage and update the cached `me` so subsequent
 * requests authenticate. For admin-changes-someone-else, the response is 204
 * (no body) — the target user's sessions are deliberately revoked.
 *
 * The dual 200-with-body / 204 contract is why this uses a raw fetch rather
 * than fetchAPI, but it still funnels 401s through handleUnauthorized() so an
 * expired session clears the token and bounces to /login like every other path.
 */
export function useChangePassword() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ userId, password }: ChangePasswordArgs) => {
      const token = getToken();
      const res = await fetch(`/api/users/${userId}/password`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ password }),
      });
      if (res.status === 401) {
        handleUnauthorized();
        throw new Error("Unauthorized");
      }
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
}
