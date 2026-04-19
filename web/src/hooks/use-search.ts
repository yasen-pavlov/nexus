import { useInfiniteQuery } from "@tanstack/react-query";
import { fetchAPI } from "@/lib/api-client";
import type { SearchResult, SearchFilters } from "@/lib/api-types";
import { searchKeys } from "@/lib/query-keys";

const PAGE_SIZE = 20;

function buildSearchURL(
  q: string,
  filters: SearchFilters,
  offset: number,
  limit: number,
): string {
  const params = new URLSearchParams({
    q,
    limit: String(limit),
    offset: String(offset),
  });
  if (filters.sources?.length) params.set("sources", filters.sources.join(","));
  if (filters.source_names?.length)
    params.set("source_names", filters.source_names.join(","));
  if (filters.date_from) params.set("date_from", filters.date_from);
  if (filters.date_to) params.set("date_to", filters.date_to);
  return `/api/search?${params}`;
}

export function useSearch(q: string, filters: SearchFilters) {
  const trimmed = q.trim();
  return useInfiniteQuery({
    queryKey: searchKeys.query(trimmed, filters),
    queryFn: ({ pageParam }) =>
      fetchAPI<SearchResult>(
        buildSearchURL(trimmed, filters, pageParam, PAGE_SIZE),
      ),
    initialPageParam: 0,
    getNextPageParam: (lastPage, allPages) => {
      const fetched = allPages.reduce(
        (n, p) => n + (p.documents?.length ?? 0),
        0,
      );
      if (fetched >= lastPage.total_count) return undefined;
      return fetched;
    },
    enabled: trimmed.length > 0,
  });
}

export { PAGE_SIZE };
