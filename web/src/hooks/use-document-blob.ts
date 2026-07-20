import { useQuery } from "@tanstack/react-query";
import { fetchAuthedBlobData } from "@/lib/api-client";
import { useObjectURL } from "@/hooks/use-object-url";

// useDocumentBlob fetches a document's binary content via the
// authenticated /documents/:id/content endpoint and returns an object
// URL suitable for <img src> or <video src>. Mirrors useAvatarBlob.
//
// The query caches the raw Blob (not the object URL): the URL is minted
// per-consumer via useObjectURL and revoked on unmount, so the cache can
// keep the Blob and re-mint a live URL on every remount. Caching the URL
// string and revoking it on unmount instead produced dead blob: URLs on
// revisit, since the cache handed back the already-revoked string.
// Returns null when the caller disables the query, the fetch 404s, or id
// isn't known.
export function useDocumentBlob(
  id: string | null | undefined,
  enabled = true,
) {
  const canQuery = Boolean(id && enabled);
  const query = useQuery<Blob | null>({
    queryKey: ["document-blob", id ?? ""],
    queryFn: () =>
      fetchAuthedBlobData(
        `/api/documents/${encodeURIComponent(id!)}/content`,
      ),
    enabled: canQuery,
    staleTime: 60 * 60 * 1000,
    retry: false,
  });

  const url = useObjectURL(query.data);
  return {
    ...query,
    data: url,
    // Stay "loading" until the object URL is minted from the fetched blob so
    // consumers don't briefly see data=null with isLoading=false.
    isLoading: query.isLoading || (query.data != null && url == null),
  };
}
