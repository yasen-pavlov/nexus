import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { fetchAuthedBlob } from "@/lib/api-client";
import { avatarKeys } from "@/lib/query-keys";

// useAvatarBlob fetches an authenticated avatar image from the
// connector endpoint and returns an object URL suitable for an <img
// src>. Revokes the URL when the query result churns so blob memory
// doesn't leak across conversation navigations. Returns null for
// sources/users without a cached avatar — caller renders initials.
export function useAvatarBlob(
  connectorID: string | null | undefined,
  externalID: string | null | undefined,
) {
  const enabled = Boolean(connectorID && externalID);

  const query = useQuery<string | null>({
    queryKey: avatarKeys.blob(connectorID ?? "", externalID ?? ""),
    queryFn: () =>
      fetchAuthedBlob(
        `/api/connectors/${encodeURIComponent(connectorID!)}/avatars/${encodeURIComponent(externalID!)}`,
      ),
    enabled,
    staleTime: 60 * 60 * 1000,
    retry: false,
  });

  useEffect(() => {
    const url = query.data;
    if (!url) return;
    return () => URL.revokeObjectURL(url);
  }, [query.data]);

  return query;
}
