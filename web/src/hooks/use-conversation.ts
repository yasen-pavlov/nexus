import { useInfiniteQuery, type InfiniteData } from "@tanstack/react-query";
import { useMemo } from "react";
import { fetchAPI } from "@/lib/api-client";
import type {
  ConversationMessagesResponse,
  Document,
} from "@/lib/api-types";
import { conversationKeys } from "@/lib/query-keys";

export interface UseConversationOptions {
  anchorTs?: string;
  pageSize?: number;
}

// ConversationPageParam discriminates the four ways we ask the backend
// for messages:
//   - tail:    no cursors, returns the conversation's latest page
//   - around:  anchor-seeded open, returns ~limit/2 messages on each
//              side of the anchor in one request
//   - before:  paginate older (scroll up)
//   - after:   paginate newer (scroll down)
//
// The initial request is `around` when an anchor is provided, `tail`
// otherwise. Subsequent pages always use before/after via the
// next_before / next_after cursors returned on each page.
export type ConversationPageParam =
  | { kind: "tail" }
  | { kind: "around"; ts: string }
  | { kind: "before"; ts: string }
  | { kind: "after"; ts: string };

const DEFAULT_PAGE_SIZE = 50;

function buildURL(
  sourceType: string,
  conversationID: string,
  param: ConversationPageParam,
  limit: number,
): string {
  const params = new URLSearchParams({ limit: String(limit) });
  if (param.kind === "around") params.set("around", param.ts);
  if (param.kind === "before") params.set("before", param.ts);
  if (param.kind === "after") params.set("after", param.ts);
  const encType = encodeURIComponent(sourceType);
  const encID = encodeURIComponent(conversationID);
  return `/api/conversations/${encType}/${encID}/messages?${params.toString()}`;
}

function dedupMessages(list: Document[]): Document[] {
  const seen = new Set<string>();
  const out: Document[] = [];
  for (const m of list) {
    if (seen.has(m.source_id)) continue;
    seen.add(m.source_id);
    out.push(m);
  }
  return out;
}

export function useConversation(
  sourceType: string,
  conversationID: string,
  { anchorTs, pageSize = DEFAULT_PAGE_SIZE }: UseConversationOptions = {},
) {
  const initialParam: ConversationPageParam = anchorTs
    ? { kind: "around", ts: anchorTs }
    : { kind: "tail" };

  const query = useInfiniteQuery<
    ConversationMessagesResponse,
    Error,
    InfiniteData<ConversationMessagesResponse, ConversationPageParam>,
    ReturnType<typeof conversationKeys.messages>,
    ConversationPageParam
  >({
    queryKey: conversationKeys.messages(sourceType, conversationID, anchorTs),
    initialPageParam: initialParam,
    queryFn: async ({ pageParam }) =>
      fetchAPI<ConversationMessagesResponse>(
        buildURL(sourceType, conversationID, pageParam, pageSize),
      ),
    // Older-direction pagination (scroll up). Walks next_before.
    getNextPageParam: (last): ConversationPageParam | undefined =>
      last.next_before ? { kind: "before", ts: last.next_before } : undefined,
    // Newer-direction pagination (scroll down). Walks next_after —
    // populated on both `around` and `after` responses, so no special
    // bootstrap is needed for anchor-seeded opens.
    getPreviousPageParam: (first): ConversationPageParam | undefined =>
      first.next_after ? { kind: "after", ts: first.next_after } : undefined,
    enabled: Boolean(sourceType && conversationID),
  });

  const messages = useMemo(() => {
    if (!query.data) return [] as Document[];
    const all: Document[] = [];
    for (const page of query.data.pages) all.push(...page.messages);
    all.sort(
      (a, b) =>
        new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
    );
    return dedupMessages(all);
  }, [query.data]);

  return {
    messages,
    isLoadingInitial: query.isPending,
    isFetchingOlder: query.isFetchingNextPage,
    isFetchingNewer: query.isFetchingPreviousPage,
    hasOlder: Boolean(query.hasNextPage),
    hasNewer: Boolean(query.hasPreviousPage),
    fetchOlder: query.fetchNextPage,
    fetchNewer: query.fetchPreviousPage,
    error: query.error,
    refetch: query.refetch,
  };
}
