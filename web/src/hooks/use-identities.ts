import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { fetchAPI } from "@/lib/api-client";
import type { IdentitiesResponse, Identity } from "@/lib/api-types";
import { identityKeys } from "@/lib/query-keys";

export interface IdentityLookup {
  identities: Identity[];
  bySourceType: Map<string, Identity>;
  byConnectorId: Map<string, Identity>;
}

const EMPTY: IdentityLookup = {
  identities: [],
  bySourceType: new Map(),
  byConnectorId: new Map(),
};

// useIdentities exposes the authenticated user's "self" identity on each
// of their connected sources. The lookup is cached globally (5 min stale)
// so every conversation mount doesn't refetch. Returns empty maps while
// loading so consumers can treat "unknown" as "not-me" without special
// null checks.
export function useIdentities(): IdentityLookup & {
  isLoading: boolean;
  error: unknown;
} {
  const query = useQuery<IdentitiesResponse>({
    queryKey: identityKeys.list(),
    queryFn: () => fetchAPI<IdentitiesResponse>("/api/me/identities"),
    staleTime: 5 * 60 * 1000,
  });

  const lookup = useMemo<IdentityLookup>(() => {
    if (!query.data) return EMPTY;
    const identities = query.data.identities ?? [];
    const bySourceType = new Map<string, Identity>();
    const byConnectorId = new Map<string, Identity>();
    for (const i of identities) {
      // Last-write-wins on duplicate source types. A user with multiple
      // Telegram accounts connected would collide here — we'll surface
      // that properly when/if the product gets there. Today, one
      // Telegram connector per user.
      bySourceType.set(i.source_type, i);
      byConnectorId.set(i.connector_id, i);
    }
    return { identities, bySourceType, byConnectorId };
  }, [query.data]);

  return {
    ...lookup,
    isLoading: query.isPending,
    error: query.error,
  };
}
