import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/_authenticated/admin/stats")({
  component: StatsPage,
});

function StatsPage() {
  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <h1 className="text-2xl font-semibold">Stats</h1>
      <p className="text-muted-foreground">
        System statistics and index overview. Coming in Phase 4.
      </p>
    </div>
  );
}
