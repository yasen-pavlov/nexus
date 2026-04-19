import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { ChevronLeft, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

import { ConnectorLogo } from "@/components/connectors/connector-logo";
import { connectorTypeLabel } from "@/components/connectors/connector-labels";
import { StatusChip, StatusLamp } from "@/components/connectors/status-lamp";
import {
  statusFromSync,
  type ConnectorStatus,
} from "@/components/connectors/status";
import { SyncProgress } from "@/components/connectors/sync-progress-bar";
import {
  ConnectorForm,
  type ConnectorFormValues,
} from "@/components/connectors/connector-form";
import { ScheduleField } from "@/components/connectors/schedule-field";
import { TelegramAuthPanel } from "@/components/connectors/telegram-auth-panel";
import { ActivityTimeline } from "@/components/connectors/activity-timeline";
import { DeleteConnectorDialog } from "@/components/connectors/delete-dialog";

import { useConnector } from "@/hooks/use-connectors";
import { useSyncJobs } from "@/hooks/use-sync-jobs";
import { useTelegramAuth } from "@/hooks/use-telegram-auth";
import { useIdentities } from "@/hooks/use-identities";
import { fetchAuthedBlob } from "@/lib/api-client";
import { useEffect } from "react";

export const Route = createFileRoute("/_authenticated/connectors/$id")({
  component: ConnectorDetailPage,
});

const TAB_LABEL: Record<string, string> = {
  config: "Config",
  schedule: "Schedule",
  identity: "Identity",
  activity: "Activity",
};

function ConnectorDetailPage() {
  const { id } = Route.useParams();
  const { user } = Route.useRouteContext();

  const { connector, runs, isLoading, updateConnector, deleteConnector } = useConnector(id);
  const { jobsByConnector, triggerSync, cancelSync, resetCursor } = useSyncJobs();
  const tgAuth = useTelegramAuth(id);
  const { byConnectorId: identitiesByConnector } = useIdentities();

  const [tab, setTab] = useState<string>("config");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [avatarUrl, setAvatarUrl] = useState<string | null>(null);
  // Panel visibility is independent of tgAuth.phase — the phase advances
  // through the mutation lifecycle, but the user's intent to "re-auth"
  // is separate. Without this, clicking Send Code could briefly flip
  // the UI to the identity card mid-flight (on status=failed or
  // authenticating) and look like nothing happened.
  const [showReconnectPanel, setShowReconnectPanel] = useState(false);

  const identity = identitiesByConnector.get(id);

  // Fetch the Telegram avatar as an authenticated blob. Revoke on unmount
  // or when the identity changes — matching the pattern in conversation
  // components.
  useEffect(() => {
    if (!identity?.has_avatar) return;
    let revoke: string | null = null;
    let cancelled = false;
    void fetchAuthedBlob(
      `/api/connectors/${identity.connector_id}/avatars/${identity.external_id}`,
    ).then((url) => {
      if (cancelled || !url) return;
      revoke = url;
      setAvatarUrl(url);
    });
    return () => {
      cancelled = true;
      if (revoke) URL.revokeObjectURL(revoke);
    };
  }, [identity]);

  if (isLoading || !connector) {
    return (
      <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col gap-4 px-6 py-8">
        <div className="h-6 w-40 animate-pulse rounded-full bg-muted" />
        <div className="h-28 animate-pulse rounded-xl border border-border bg-card" />
      </div>
    );
  }

  const job = jobsByConnector.get(connector.id);
  const running = job?.status === "running";
  const fallback: ConnectorStatus = connector.enabled ? "idle" : "disabled";
  const status = statusFromSync(job?.status, fallback);
  const hasIdentityTab = connector.type === "telegram";
  const canManage = user.role === "admin" || !connector.shared;

  const tabValues: string[] = ["config", "schedule"];
  if (hasIdentityTab) tabValues.push("identity");
  tabValues.push("activity");

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col gap-6 px-6 py-8">
      <Link
        to="/connectors"
        className="inline-flex w-fit items-center gap-1 text-[12px] text-muted-foreground hover:text-foreground"
      >
        <ChevronLeft className="h-3 w-3" />
        All connectors
      </Link>

      <header className="relative overflow-hidden rounded-xl border border-border bg-card">
        <span
          aria-hidden
          className="absolute inset-x-0 top-0 h-[3px]"
          style={{
            backgroundColor: `var(--source-${connector.type}, var(--source-default))`,
          }}
        />
        <div className="flex flex-col gap-5 p-6 pt-7 md:flex-row md:items-start md:justify-between">
          <div className="flex gap-4">
            <ConnectorLogo type={connector.type} size="xl" />
            <div>
              <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
                {connectorTypeLabel(connector.type)}
              </div>
              <h1 className="mt-0.5 text-[22px] font-medium tracking-[-0.01em] text-foreground">
                {connector.name}
              </h1>
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <StatusChip status={status} hint={lastRunHint(connector.last_run)} />
                {connector.shared && (
                  <span className="rounded-full border border-border bg-muted/40 px-2 py-0.5 text-[11px] text-muted-foreground">
                    Shared
                  </span>
                )}
                {identity && (
                  <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-2 py-0.5 text-[11px]">
                    {avatarUrl ? (
                      <img
                        src={avatarUrl}
                        alt=""
                        className="h-4 w-4 rounded-full object-cover"
                      />
                    ) : (
                      <StatusLamp status="succeeded" />
                    )}
                    <span className="text-foreground">{identity.external_name}</span>
                  </span>
                )}
              </div>
            </div>
          </div>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-2">
            {running ? (
              <Button
                variant="outline"
                onClick={() => job && void cancelSync(job.id)}
                className="gap-1.5"
              >
                Cancel sync
              </Button>
            ) : (
              <Button
                onClick={() => void triggerSync(connector.id)}
                disabled={!connector.enabled || !canManage}
                className="gap-1.5"
              >
                Sync now
              </Button>
            )}
            <Button
              variant="ghost"
              className="gap-1.5 text-muted-foreground hover:text-destructive"
              disabled={!canManage}
              onClick={() => setDeleteOpen(true)}
            >
              <Trash2 className="h-3.5 w-3.5" />
              Remove
            </Button>
          </div>
        </div>
        {running && job && (
          <div className="border-t border-border px-6 py-3">
            <SyncProgress
              processed={job.docs_processed}
              total={job.docs_total}
              errors={job.errors}
            />
          </div>
        )}
      </header>

      <Tabs value={tab} onValueChange={(v) => setTab(String(v ?? "config"))} className="flex flex-col gap-6">
        <TabsList variant="line" className="h-auto justify-start gap-1 bg-transparent p-0">
          {tabValues.map((t) => (
            <TabsTrigger
              key={t}
              value={t}
              className="relative rounded-md px-3 py-1.5 text-[13px] font-medium"
            >
              {TAB_LABEL[t]}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="config">
          <div className="overflow-hidden rounded-xl border border-border bg-card">
            <ConnectorForm
              mode="edit"
              initial={{
                type: connector.type,
                name: connector.name,
                enabled: connector.enabled,
                shared: connector.shared,
                schedule: connector.schedule,
                config: connector.config,
              } as ConnectorFormValues}
              onSubmit={async (values) => {
                await updateConnector(values);
                toast.success("Connector updated.");
              }}
              onCancel={() => setTab("config")}
              submitLabel="Save changes"
              isAdmin={user.role === "admin"}
            />
          </div>
        </TabsContent>

        <TabsContent value="schedule">
          <div className="space-y-4 rounded-xl border border-border bg-card p-6">
            <ScheduleField
              value={connector.schedule}
              onChange={async (next) => {
                await updateConnector({
                  type: connector.type,
                  name: connector.name,
                  enabled: connector.enabled,
                  shared: connector.shared,
                  schedule: next,
                  config: connector.config,
                });
                toast.success("Schedule updated.");
              }}
            />
            <Separator />
            <div className="flex items-center justify-between text-[12.5px] text-muted-foreground">
              <span>Force a full re-sync if indexed data looks stale.</span>
              <Button
                variant="ghost"
                size="sm"
                disabled={!canManage}
                onClick={() => void resetCursor(connector.id)}
              >
                Clear cursor
              </Button>
            </div>
          </div>
        </TabsContent>

        {hasIdentityTab && (
          <TabsContent value="identity">
            {identity && !showReconnectPanel ? (
              <div className="flex items-center gap-4 rounded-xl border border-border bg-card p-5">
                {avatarUrl ? (
                  <img
                    src={avatarUrl}
                    alt=""
                    className="h-14 w-14 rounded-full object-cover ring-1 ring-border"
                  />
                ) : (
                  <div className="h-14 w-14 rounded-full bg-muted ring-1 ring-border" />
                )}
                <div className="flex-1">
                  <div className="text-[14px] font-medium text-foreground">
                    {identity.external_name}
                  </div>
                  <div className="text-[12px] text-muted-foreground">
                    ID {identity.external_id}
                  </div>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    tgAuth.reset();
                    setShowReconnectPanel(true);
                  }}
                  disabled={!canManage}
                >
                  Reconnect
                </Button>
              </div>
            ) : (
              <TelegramAuthPanel
                phone={String(connector.config?.phone ?? "")}
                onStart={tgAuth.start}
                onSubmit={async (payload) => {
                  await tgAuth.submit(payload);
                  // On successful verify the /me/identities refetch
                  // populates the new identity; collapse the panel.
                  setShowReconnectPanel(false);
                }}
                status={tgAuth.phase}
                error={tgAuth.error}
                needs2FA={tgAuth.needs2FA}
              />
            )}
          </TabsContent>
        )}

        <TabsContent value="activity">
          <ActivityTimeline runs={runs} />
        </TabsContent>
      </Tabs>

      <DeleteConnectorDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        connectorName={connector.name}
        connectorType={connector.type}
        onConfirm={async () => {
          await deleteConnector();
          toast.success(`Removed “${connector.name}”.`);
          setDeleteOpen(false);
        }}
      />
    </div>
  );
}

function lastRunHint(iso: string | undefined): string | undefined {
  if (!iso) return undefined;
  const d = new Date(iso);
  const mins = Math.floor((Date.now() - d.getTime()) / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return d.toLocaleDateString();
}
