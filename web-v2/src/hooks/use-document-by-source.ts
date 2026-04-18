import { useQuery } from "@tanstack/react-query";
import { fetchAPI } from "@/lib/api-client";
import type { Document } from "@/lib/api-types";
import { documentKeys } from "@/lib/query-keys";

// useDocumentBySource lazily resolves a (source_type, source_id) pair
// into a full Document. The conversation view uses this for reply-quote
// targets that aren't part of the loaded message window. Callers
// control `enabled` so the fetch only fires when the target is actually
// needed on-screen. Stale time is generous — messages don't change.
export function useDocumentBySource(
  sourceType: string | null | undefined,
  sourceID: string | null | undefined,
  enabled = true,
) {
  const canQuery = Boolean(sourceType && sourceID && enabled);

  return useQuery<Document>({
    queryKey: documentKeys.bySource(sourceType ?? "", sourceID ?? ""),
    queryFn: () => {
      const params = new URLSearchParams({
        source_type: sourceType!,
        source_id: sourceID!,
      });
      return fetchAPI<Document>(
        `/api/documents/by-source?${params.toString()}`,
      );
    },
    enabled: canQuery,
    staleTime: 10 * 60 * 1000,
    retry: false,
  });
}
