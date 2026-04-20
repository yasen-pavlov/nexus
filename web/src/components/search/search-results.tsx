import { useNavigate } from "@tanstack/react-router";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { DocumentHit, Facet } from "@/lib/api-types";
import { useSearch } from "@/hooks/use-search";
import { useDocumentDownload } from "@/hooks/use-document-download";
import { type SearchParams, paramsToFilters } from "@/lib/search-params";
import { ResultCard } from "./result-card";
import { NoResultsState, WelcomeState } from "./empty-states";
import { SearchFilters } from "./search-filters";

// Boundary cast: useNavigate without `from` types `search` as never. Our
// typed payloads still flow through SearchParams.
type AnyNavigate = (opts: {
  to?: string;
  params?: Record<string, string>;
  search?:
    | SearchParams
    | { anchor_id?: number; anchor_ts?: string }
    | undefined;
  replace?: boolean;
}) => void;

interface Props {
  params: SearchParams;
}

export function SearchResults({ params }: Readonly<Props>) {
  const navigate = useNavigate() as unknown as AnyNavigate;
  const query = params.q?.trim() ?? "";
  const filters = paramsToFilters(params);

  const {
    data,
    isLoading,
    isError,
    error,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useSearch(query, filters);

  const download = useDocumentDownload();

  const hits: DocumentHit[] =
    data?.pages.flatMap((p) => p.documents ?? []) ?? [];
  const total = data?.pages[0]?.total_count ?? 0;
  const facets: Record<string, Facet[]> | undefined = data?.pages[0]?.facets;

  const openChat = (hit: DocumentHit) => {
    if (!hit.conversation_id) return;

    // Precision anchor: when the search pipeline pinpointed the exact
    // matching message inside a window, jump to that message. Falls
    // back to the window's connector-emitted anchor (first message),
    // then to the hit's own created_at for timestamp.
    let anchorID: number | undefined;
    if (typeof hit.match_message_id === "number") {
      anchorID = hit.match_message_id;
    } else if (typeof hit.metadata?.anchor_message_id === "number") {
      anchorID = hit.metadata.anchor_message_id;
    } else if (typeof hit.metadata?.message_id === "number") {
      anchorID = hit.metadata.message_id;
    }

    let anchorTs: string | undefined;
    if (typeof hit.match_created_at === "string") {
      anchorTs = hit.match_created_at;
    } else if (typeof hit.metadata?.anchor_created_at === "string") {
      anchorTs = hit.metadata.anchor_created_at;
    } else {
      anchorTs = hit.created_at;
    }

    navigate({
      to: "/conversations/$sourceType/$conversationId",
      params: {
        sourceType: hit.source_type,
        conversationId: hit.conversation_id,
      },
      search:
        anchorID !== undefined || anchorTs !== undefined
          ? { anchor_id: anchorID, anchor_ts: anchorTs }
          : undefined,
    });
  };

  const doDownload = (hit: DocumentHit) => {
    download.mutate(
      { id: hit.id, suggestedFilename: hit.title },
      {
        onError: (err) => {
          toast.error(err instanceof Error ? err.message : "Download failed");
        },
      },
    );
  };

  const openRelated = (doc: DocumentHit) => {
    // Telegram docs with a conversation_id open the chat browser; others
    // rerun search with the doc's title as a fallback (no doc viewer yet).
    if (doc.source_type === "telegram" && doc.conversation_id) {
      openChat(doc);
      return;
    }
    const q = doc.title || doc.source_id;
    navigate({
      search: { ...params, q },
      replace: false,
    });
  };

  if (!query) {
    return (
      <WelcomeState
        onPickExample={(q) =>
          navigate({ search: { ...params, q }, replace: false })
        }
      />
    );
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
        <Loader2 className="mr-2 size-4 animate-spin" aria-hidden />
        Searching…
      </div>
    );
  }

  if (isError) {
    return (
      <div className="mx-auto max-w-xl py-8 text-sm text-destructive">
        {error instanceof Error ? error.message : "Search failed"}
      </div>
    );
  }

  if (hits.length === 0) {
    return <NoResultsState query={query} />;
  }

  return (
    <div className="flex flex-col gap-4">
      <SearchFilters params={params} facets={facets} />

      <div className="text-[12px] text-muted-foreground">
        <span className="tabular-nums text-foreground">{total}</span>
        <span className="ml-1">result{total === 1 ? "" : "s"} for</span>{" "}
        <span className="text-foreground">&ldquo;{query}&rdquo;</span>
      </div>

      <div className="flex flex-col gap-3">
        {hits.map((hit) => (
          <ResultCard
            key={hit.id}
            hit={hit}
            onOpenChat={openChat}
            onDownload={doDownload}
            onNavigateRelated={openRelated}
          />
        ))}
      </div>

      {hasNextPage && (
        <div className="flex justify-center py-4">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              fetchNextPage();
            }}
            disabled={isFetchingNextPage}
          >
            {isFetchingNextPage ? (
              <>
                <Loader2 className="mr-2 size-4 animate-spin" aria-hidden />
                Loading…
              </>
            ) : (
              "Load more"
            )}
          </Button>
        </div>
      )}
    </div>
  );
}
