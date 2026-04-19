import { useState } from "react";
import { Flame, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ConnectorLogo } from "@/components/connectors/connector-logo";
import { SourceChip } from "@/components/source-chip";

import { TypedConfirmDialog } from "@/components/admin/typed-confirm-dialog";

import { useStorageStats } from "@/hooks/use-storage-stats";
import { useConnectors } from "@/hooks/use-connectors";
import type { BinaryStoreStats } from "@/lib/api-types";
import { formatBytes, formatCount } from "@/lib/format";
import { cn } from "@/lib/utils";

/**
 * Per-connector binary cache rows + a wipe-all footer. Telegram rows
 * surface an extra warning plaque in the typed-confirm dialog because
 * Telegram media is eager-cached — wiping can lose content that's no
 * longer fetchable from the upstream.
 */
export function CacheSection() {
  const { data, isPending, wipeAll, wipeByConnector } = useStorageStats();
  const connectors = useConnectors();

  const [pendingWipe, setPendingWipe] = useState<BinaryStoreStats | null>(null);
  const [confirmWipeAll, setConfirmWipeAll] = useState(false);

  if (isPending) {
    return (
      <div className="flex flex-col gap-3">
        <Skeleton className="h-14 w-full" />
        <Skeleton className="h-14 w-full" />
      </div>
    );
  }

  const rows = data ?? [];

  if (rows.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border/70 bg-background/40 px-4 py-8 text-center text-[12.5px] text-muted-foreground/80">
        No cached binaries yet — attachments and media get stored here after
        the first time a connector fetches them.
      </div>
    );
  }

  const totalBytes = rows.reduce((s, r) => s + r.total_size, 0);
  const totalCount = rows.reduce((s, r) => s + r.count, 0);

  const connectorIdFor = (row: BinaryStoreStats): string | undefined => {
    // Match (source_type, source_name) back to the connector so the
    // per-row wipe can call DELETE /api/storage/cache/{connectorID}.
    const match = connectors.connectors.find(
      (c) => c.type === row.source_type && c.name === row.source_name,
    );
    return match?.id;
  };

  return (
    <div className="flex flex-col gap-4">
      <ul className="flex flex-col divide-y divide-border/60 rounded-md border border-border/70 bg-background/40">
        {rows.map((row) => {
          const connectorId = connectorIdFor(row);
          return (
            <li
              key={`${row.source_type}/${row.source_name}`}
              className="relative flex items-center gap-3 px-4 py-3"
            >
              {/* Tonal spine */}
              <span
                aria-hidden
                style={
                  {
                    backgroundColor: `color-mix(in oklch, var(--source-${row.source_type}, var(--source-default)) 55%, transparent)`,
                  } as React.CSSProperties
                }
                className="absolute left-0 top-2 bottom-2 w-[2px] rounded-full"
              />
              <ConnectorLogo type={row.source_type} size="sm" />
              <div className="flex min-w-0 flex-1 flex-col leading-tight">
                <div className="flex items-center gap-2">
                  <SourceChip type={row.source_type} />
                  <span className="truncate text-[13.5px] font-medium text-foreground">
                    {row.source_name}
                  </span>
                </div>
                <div className="mt-0.5 text-[12px] tabular-nums text-muted-foreground">
                  {formatCount(row.count)}{" "}
                  {row.count === 1 ? "file" : "files"} ·{" "}
                  {formatBytes(row.total_size)}
                </div>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className={cn(
                  "gap-1.5",
                  !connectorId && "opacity-60",
                )}
                onClick={() => setPendingWipe(row)}
                disabled={!connectorId}
                title={
                  connectorId
                    ? undefined
                    : "No matching connector — wipe all instead"
                }
              >
                <Trash2 className="h-3.5 w-3.5" />
                Wipe
              </Button>
            </li>
          );
        })}
      </ul>

      <div className="flex items-center justify-between rounded-md border border-destructive/40 bg-destructive/5 px-4 py-3">
        <div className="min-w-0">
          <div className="text-[13px] font-medium text-foreground">
            Wipe everything
          </div>
          <div className="text-[12px] text-muted-foreground">
            {formatCount(totalCount)} cached{" "}
            {totalCount === 1 ? "file" : "files"} across {rows.length}{" "}
            source{rows.length === 1 ? "" : "s"} — {formatBytes(totalBytes)}.
          </div>
        </div>
        <Button
          type="button"
          variant="destructive"
          size="sm"
          className="gap-1.5"
          onClick={() => setConfirmWipeAll(true)}
        >
          <Flame className="h-3.5 w-3.5" />
          Wipe all
        </Button>
      </div>

      {pendingWipe && (
        <TypedConfirmDialog
          open
          onOpenChange={(v) => {
            if (!v) setPendingWipe(null);
          }}
          eyebrow={pendingWipe.source_type === "telegram" ? "Eager cache" : "Wipe cache"}
          title={`Wipe ${pendingWipe.source_name}'s cache?`}
          body={
            <div className="flex flex-col gap-2">
              <span>
                Removes{" "}
                <span className="font-medium text-foreground">
                  {formatCount(pendingWipe.count)}
                </span>{" "}
                cached{" "}
                {pendingWipe.count === 1 ? "file" : "files"} (
                {formatBytes(pendingWipe.total_size)}).
              </span>
              {pendingWipe.source_type === "telegram" && (
                <span className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-destructive">
                  Telegram eager-caches media on sync. Some attachments may
                  not be re-fetchable if the upstream expired — verify you
                  don&apos;t need any of these files before continuing.
                </span>
              )}
            </div>
          }
          confirmPhrase={pendingWipe.source_name}
          confirmLabel="Wipe cache"
          variant="destructive"
          onConfirm={async () => {
            const id = connectorIdFor(pendingWipe);
            if (!id) {
              setPendingWipe(null);
              return;
            }
            await wipeByConnector.mutateAsync(id);
            setPendingWipe(null);
          }}
        />
      )}

      <TypedConfirmDialog
        open={confirmWipeAll}
        onOpenChange={setConfirmWipeAll}
        eyebrow="Danger zone"
        title="Wipe the entire binary cache?"
        body={
          <span>
            Removes{" "}
            <span className="font-medium text-foreground">
              {formatCount(totalCount)}
            </span>{" "}
            cached files ({formatBytes(totalBytes)}) across every connector.
            Eager-cached media that isn&apos;t re-fetchable upstream is lost
            permanently.
          </span>
        }
        confirmPhrase="wipe everything"
        confirmLabel="Wipe all caches"
        variant="destructive"
        onConfirm={async () => {
          await wipeAll.mutateAsync();
          setConfirmWipeAll(false);
        }}
      />
    </div>
  );
}
