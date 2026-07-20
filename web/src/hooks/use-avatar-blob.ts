import { useQuery } from "@tanstack/react-query";
import { fetchAuthedBlobData } from "@/lib/api-client";
import { avatarKeys } from "@/lib/query-keys";
import { useObjectURL } from "@/hooks/use-object-url";

// useAvatarBlob fetches an authenticated avatar image from the connector
// endpoint and returns an object URL suitable for an <img src>.
//
// The query caches the raw Blob; the object URL is minted per-consumer via
// useObjectURL and revoked on unmount. This keeps the URL's lifetime tied to
// the DOM node rather than the cache entry, so two components sharing the
// avatar don't revoke each other's URL and a remount within the cache window
// re-mints a live URL instead of returning a revoked one. Returns null for
// sources/users without a cached avatar — caller renders initials.
export function useAvatarBlob(
  connectorID: string | null | undefined,
  externalID: string | null | undefined,
) {
  const enabled = Boolean(connectorID && externalID);

  const query = useQuery<Blob | null>({
    queryKey: avatarKeys.blob(connectorID ?? "", externalID ?? ""),
    queryFn: () =>
      fetchAuthedBlobData(
        `/api/connectors/${encodeURIComponent(connectorID!)}/avatars/${encodeURIComponent(externalID!)}`,
      ),
    enabled,
    staleTime: 60 * 60 * 1000,
    retry: false,
  });

  const url = useObjectURL(query.data);
  return {
    ...query,
    data: url,
    isLoading: query.isLoading || (query.data != null && url == null),
  };
}
