import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/_authenticated/")({
  component: SearchPage,
});

function SearchPage() {
  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <h1 className="text-2xl font-semibold">Search</h1>
      <p className="text-muted-foreground">
        Search across all your data sources. Coming in Phase 1.
      </p>
    </div>
  );
}
