import { useQuery } from "@tanstack/react-query";
import { fetchAPI } from "@/lib/api-client";
import type { RelatedResponse } from "@/lib/api-types";
import { documentKeys } from "@/lib/query-keys";

export function useRelated(docID: string, enabled: boolean) {
  return useQuery({
    queryKey: documentKeys.related(docID),
    queryFn: () =>
      fetchAPI<RelatedResponse>(`/api/documents/${docID}/related`),
    enabled,
  });
}
