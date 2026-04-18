import { createFileRoute } from "@tanstack/react-router";
import { SearchBar } from "@/components/search/search-bar";
import { SearchResults } from "@/components/search/search-results";
import { NoConnectorsState } from "@/components/search/empty-states";
import { useConnectors } from "@/hooks/use-connectors";
import { searchParamsSchema } from "@/lib/search-params";

export const Route = createFileRoute("/_authenticated/")({
  validateSearch: searchParamsSchema,
  component: SearchPage,
});

function SearchPage() {
  const params = Route.useSearch();
  const { data: connectors, isLoading: loadingConnectors } = useConnectors();
  const { user } = Route.useRouteContext();

  // Show "add a connector" only when we're sure the user has zero
  // connectors they can modify. Admins see shared connectors too, so a
  // freshly installed admin starts with the seeded filesystem connector
  // and skips this state.
  const ownedOrShared =
    connectors?.filter(
      (c) => c.shared || (c.user_id && c.user_id === user.id),
    ) ?? [];
  const showNoConnectors =
    !loadingConnectors && ownedOrShared.length === 0 && !params.q;

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-4 p-4 md:p-6">
      <SearchBar params={params} />
      {showNoConnectors ? (
        <NoConnectorsState />
      ) : (
        <SearchResults params={params} />
      )}
    </div>
  );
}
