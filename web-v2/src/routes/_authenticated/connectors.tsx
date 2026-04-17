import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/_authenticated/connectors")({
  component: ConnectorsPage,
});

function ConnectorsPage() {
  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <h1 className="text-2xl font-semibold">Connectors</h1>
      <p className="text-muted-foreground">
        Manage your data source connectors. Coming in Phase 3.
      </p>
    </div>
  );
}
