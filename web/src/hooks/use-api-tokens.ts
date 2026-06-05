import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import type {
  ApiToken,
  CreateTokenRequest,
  CreateTokenResponse,
} from "@/lib/api-types";
import { apiTokenKeys } from "@/lib/query-keys";

export function useApiTokens() {
  const qc = useQueryClient();

  const query = useQuery<ApiToken[]>({
    queryKey: apiTokenKeys.list(),
    queryFn: () => fetchAPI<ApiToken[]>("/api/tokens"),
    staleTime: 30_000,
  });

  // The mutation resolves with the plaintext-bearing response so the caller
  // can show the secret once. Cache invalidation refreshes the list (which
  // never carries the secret).
  const create = useMutation({
    mutationFn: (args: CreateTokenRequest) =>
      fetchAPI<CreateTokenResponse>("/api/tokens", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(args),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: apiTokenKeys.list() });
      toast.success("Token created");
    },
    onError: (err: Error) => toast.error(err.message || "Create failed"),
  });

  const revoke = useMutation({
    mutationFn: (id: string) =>
      fetchAPI<void>(`/api/tokens/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: apiTokenKeys.list() });
      toast.success("Token revoked");
    },
    onError: (err: Error) => toast.error(err.message || "Revoke failed"),
  });

  return { ...query, create, revoke };
}
