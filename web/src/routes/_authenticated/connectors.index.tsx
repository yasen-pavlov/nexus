import { createFileRoute } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";

import {
  ConnectorCard,
  type ConnectorRow,
} from "@/components/connectors/connector-card";
import {
  ConnectorsEmpty,
  ConnectorsLoading,
} from "@/components/connectors/connector-list-states";
import { ConnectorForm } from "@/components/connectors/connector-form";
import { DeleteConnectorDialog } from "@/components/connectors/delete-dialog";
import {
  statusFromSync,
  type ConnectorStatus,
} from "@/components/connectors/status";

import { useConnectors } from "@/hooks/use-connectors";
import { useSyncJobs } from "@/hooks/use-sync-jobs";
import { useIdentities } from "@/hooks/use-identities";

export const Route = createFileRoute("/_authenticated/connectors/")({
  component: ConnectorsPage,
});

function ConnectorsPage() {
  const { user } = Route.useRouteContext();
  const {
    connectors,
    isLoading,
    createConnector,
    updateConnector,
    deleteConnector,
  } = useConnectors();
  const { triggerSync, cancelSync, triggerAll, resetCursor, jobsByConnector } = useSyncJobs();
  const { byConnectorId: identitiesByConnector } = useIdentities();

  const [sheetOpen, setSheetOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<ConnectorRow | null>(null);

  const anyRunning = useMemo(
    () =>
      connectors.some((c) => {
        const j = jobsByConnector.get(c.id);
        return j?.status === "running";
      }),
    [connectors, jobsByConnector],
  );

  const rows: ConnectorRow[] = connectors.map((c) => {
    const job = jobsByConnector.get(c.id);
    const fallback: ConnectorStatus = c.enabled ? "idle" : "disabled";
    const identity = identitiesByConnector.get(c.id);
    return {
      id: c.id,
      type: c.type,
      name: c.name,
      status: statusFromSync(job?.status, fallback),
      enabled: c.enabled,
      shared: c.shared,
      schedule: c.schedule,
      lastRun: c.last_run || undefined,
      identity: identity
        ? {
            name: identity.external_name || "Connected",
            connectorId: identity.connector_id,
            externalId: identity.external_id,
            hasAvatar: identity.has_avatar,
          }
        : undefined,
      sync:
        job?.status === "running"
          ? {
              jobId: job.id,
              processed: job.docs_processed,
              total: job.docs_total,
              errors: job.errors,
              scope: job.scope,
            }
          : undefined,
    };
  });

  let listSection: React.ReactNode;
  if (isLoading) {
    listSection = <ConnectorsLoading />;
  } else if (rows.length === 0) {
    listSection = <ConnectorsEmpty onAdd={() => setSheetOpen(true)} />;
  } else {
    listSection = (
      <ul className="flex flex-col gap-3">
        {rows.map((row) => (
          <li key={row.id}>
            <ConnectorCard
              row={row}
              onSync={(id) => void triggerSync(id)}
              onCancel={(jobId) => void cancelSync(jobId)}
              onResetCursor={(id) => void resetCursor(id)}
              onDelete={(id) => {
                const r = rows.find((x) => x.id === id);
                if (r) setPendingDelete(r);
              }}
              onToggleShared={
                user.role === "admin"
                  ? (id, next) => {
                      const cfg = connectors.find((c) => c.id === id);
                      if (!cfg) return;
                      updateConnector({
                        id: cfg.id,
                        type: cfg.type,
                        name: cfg.name,
                        config: cfg.config,
                        enabled: cfg.enabled,
                        schedule: cfg.schedule,
                        shared: next,
                      });
                    }
                  : undefined
              }
              canManage={user.role === "admin" || !row.shared}
            />
          </li>
        ))}
      </ul>
    );
  }

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col gap-6 px-6 py-8">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-baseline sm:justify-between sm:gap-4">
        <div className="min-w-0">
          <h1 className="text-[20px] font-medium tracking-[-0.005em] text-foreground">
            Connectors
          </h1>
          <p className="mt-0.5 text-[13px] text-muted-foreground">
            Your sources of indexed content. Manage schedules, credentials, and sync runs.
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {rows.length > 0 && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void triggerAll();
                toast.info("Starting sync for all enabled connectors…");
              }}
              disabled={anyRunning}
              className="gap-1.5"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${anyRunning ? "animate-spin" : ""}`} />
              Sync all
            </Button>
          )}
          <Button onClick={() => setSheetOpen(true)} className="gap-1.5">
            <Plus className="h-3.5 w-3.5" />
            Add connector
          </Button>
        </div>
      </header>

      {listSection}

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent side="right" className="w-full p-0 sm:max-w-xl">
          <SheetHeader className="border-b border-border px-6 py-4">
            <SheetTitle className="text-[15px] font-medium">New connector</SheetTitle>
          </SheetHeader>
          <ConnectorForm
            mode="create"
            isAdmin={user.role === "admin"}
            onSubmit={async (values) => {
              await createConnector(values);
              toast.success(`Connector “${values.name}” created.`);
              setSheetOpen(false);
            }}
            onCancel={() => setSheetOpen(false)}
          />
        </SheetContent>
      </Sheet>

      {pendingDelete && (
        <DeleteConnectorDialog
          open
          onOpenChange={(v) => !v && setPendingDelete(null)}
          connectorName={pendingDelete.name}
          connectorType={pendingDelete.type}
          onConfirm={async () => {
            await deleteConnector(pendingDelete.id);
            toast.success(`Removed “${pendingDelete.name}”.`);
            setPendingDelete(null);
          }}
        />
      )}
    </div>
  );
}
