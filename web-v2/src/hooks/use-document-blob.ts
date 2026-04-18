import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { fetchAuthedBlob } from "@/lib/api-client";

// useDocumentBlob fetches a document's binary content via the
// authenticated /documents/:id/content endpoint and returns an object
// URL suitable for <img src> or <video src>. Mirrors useAvatarBlob —
// revokes the URL when the query churns so blob memory doesn't leak
// across message list updates. Returns null when the caller disables
// the query, the fetch 404s, or id isn't known.
export function useDocumentBlob(
  id: string | null | undefined,
  enabled = true,
) {
  const canQuery = Boolean(id && enabled);
  const query = useQuery<string | null>({
    queryKey: ["document-blob", id ?? ""],
    queryFn: () =>
      fetchAuthedBlob(
        `/api/documents/${encodeURIComponent(id!)}/content`,
      ),
    enabled: canQuery,
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
