import type { CSSProperties } from "react";
import { Link } from "@tanstack/react-router";
import { MoreHorizontal, Play, Square, RotateCcw, Share2 } from "lucide-react";
import { formatDistanceToNow } from "date-fns";
import cronstrue from "cronstrue";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useAvatarBlob } from "@/hooks/use-avatar-blob";

import { ConnectorLogo, connectorTypeLabel } from "./connector-logo";
import { StatusLamp, type ConnectorStatus } from "./status-lamp";
import { SyncProgress } from "./sync-progress-bar";

export interface ConnectorRow {
  id: string;
  type: string;
  name: string;
  status: ConnectorStatus;
  enabled: boolean;
  shared: boolean;
  schedule: string;
  lastRun?: string;
  // The card fetches the avatar blob itself — the list page only needs
  // to hand over the identity keys. Saves passing N blob URLs through
  // parent state and keeps revocation lifecycle inside useAvatarBlob.
  identity?: { name: string; connectorId?: string; externalId?: string; hasAvatar?: boolean };
  sync?: {
    jobId: string;
    processed: number;
    total: number;
    errors: number;
  };
}

export interface ConnectorCardProps {
  row: ConnectorRow;
  onSync: (id: string) => void;
  onCancel: (jobId: string) => void;
  onResetCursor: (id: string) => void;
  onDelete: (id: string) => void;
  /** Admin-only: toggle shared flag. Undefined hides the menu item. */
  onToggleShared?: (id: string, next: boolean) => void;
  canManage: boolean;
}

/**
 * The specimen row. Left tonal stripe keyed to source hue + brass logo
 * plate + status lamp + dense meta + right-aligned action tray. When the
 * connector is actively syncing, a thin marmalade progress bar appears
 * inside the card body; the left stripe gets a soft pulse to draw the
 * eye without being a spinner.
 */
export function ConnectorCard({
  row,
  onSync,
  onCancel,
  onResetCursor,
  onDelete,
  onToggleShared,
  canManage,
}: ConnectorCardProps) {
  const scheduleLabel = row.schedule
    ? safeCronstrue(row.schedule)
    : row.enabled
      ? "Manual trigger"
      : "Disabled";

  const running = row.status === "running";

  return (
    <article
      className={cn(
        "group relative overflow-hidden rounded-lg border border-border bg-card",
        "transition-colors duration-200 hover:bg-card-hover hover:border-accent-foreground/20",
        running && "border-primary/30",
      )}
    >
      <span
        aria-hidden
        style={
          {
            backgroundColor: `var(--source-${row.type}, var(--source-default))`,
          } as CSSProperties
        }
        className={cn(
          "absolute inset-y-0 left-0 w-[3px]",
          running && "specimen-pulse",
        )}
      />

      <div className="flex flex-wrap items-start gap-x-4 gap-y-3 p-4 pl-5">
        <Link
          to="/connectors/$id"
          params={{ id: row.id }}
          aria-label={`Open ${row.name}`}
        >
          <ConnectorLogo type={row.type} size="lg" />
        </Link>

        <div className="min-w-0 flex-1 basis-[180px]">
          <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
            <Link
              to="/connectors/$id"
              params={{ id: row.id }}
              className="truncate text-[15px] font-medium tracking-[-0.005em] text-foreground decoration-primary/60 underline-offset-4 hover:underline"
            >
              {row.name}
            </Link>
            <StatusLamp status={row.status} />
            {row.shared && (
              <span className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/40 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                <Share2 className="h-2.5 w-2.5" /> Shared
              </span>
            )}
            {!row.enabled && (
              <span className="rounded-full border border-border bg-muted/40 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Disabled
              </span>
            )}
          </div>

          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-muted-foreground">
            <span>{connectorTypeLabel(row.type)}</span>
            {row.identity && <IdentityInline identity={row.identity} />}
            <span className="flex items-center gap-1">
              <ScheduleGlyph /> {scheduleLabel}
            </span>
            {row.lastRun && !running && (
              <span>
                Last run {formatDistanceToNow(new Date(row.lastRun), { addSuffix: true })}
              </span>
            )}
          </div>

          {running && row.sync && (
            <div className="mt-3">
              <SyncProgress
                processed={row.sync.processed}
                total={row.sync.total}
                errors={row.sync.errors}
              />
            </div>
          )}

          {row.status === "failed" && (
            <p className="mt-2 text-[12px] text-destructive">
              Last sync failed. Open the connector to see the error.
            </p>
          )}
        </div>

        <div className="ml-auto flex shrink-0 items-center gap-1.5">
          {running ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => row.sync && onCancel(row.sync.jobId)}
              className="h-8 gap-1.5"
            >
              <Square className="h-3 w-3 fill-current" /> Cancel
            </Button>
          ) : (
            <Button
              variant="outline"
              size="sm"
              onClick={() => onSync(row.id)}
              disabled={!row.enabled || !canManage}
              className="h-8 gap-1.5"
            >
              <Play className="h-3 w-3 fill-current" /> Sync
            </Button>
          )}
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button variant="ghost" size="icon" className="h-8 w-8" aria-label="More actions">
                  <MoreHorizontal className="h-4 w-4" />
                </Button>
              }
            />
            <DropdownMenuContent align="end" className="w-52">
              <DropdownMenuItem render={<Link to="/connectors/$id" params={{ id: row.id }} />}>
                Open connector
              </DropdownMenuItem>
              <DropdownMenuItem
                disabled={!canManage}
                onClick={() => onResetCursor(row.id)}
              >
                <RotateCcw className="mr-2 h-3.5 w-3.5" />
                Force full re-sync
              </DropdownMenuItem>
              {onToggleShared && (
                <DropdownMenuItem
                  disabled={!canManage}
                  onClick={() => onToggleShared(row.id, !row.shared)}
                >
                  <Share2 className="mr-2 h-3.5 w-3.5" />
                  {row.shared ? "Make private" : "Share with users"}
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem
                disabled={!canManage}
                className="text-destructive focus:text-destructive"
                onClick={() => onDelete(row.id)}
              >
                Remove connector…
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </article>
  );
}

function IdentityInline({
  identity,
}: {
  identity: NonNullable<ConnectorRow["identity"]>;
}) {
  // Only subscribe to the avatar endpoint when the backend says there's
  // one cached — otherwise useAvatarBlob stays disabled and we skip a
  // guaranteed-404.
  const { data: avatarUrl } = useAvatarBlob(
    identity.hasAvatar ? identity.connectorId : null,
    identity.hasAvatar ? identity.externalId : null,
  );
  return (
    <span className="inline-flex items-center gap-1.5">
      {avatarUrl ? (
        <img
          src={avatarUrl}
          alt=""
          className="h-4 w-4 rounded-full object-cover ring-1 ring-border"
        />
      ) : (
        <span className="h-4 w-4 rounded-full bg-muted ring-1 ring-border" />
      )}
      <span className="truncate">{identity.name}</span>
    </span>
  );
}

function ScheduleGlyph() {
  return (
    <svg viewBox="0 0 16 16" className="h-3 w-3" fill="none" aria-hidden>
      <circle cx="8" cy="8" r="6.5" stroke="currentColor" strokeOpacity="0.7" />
      <path d="M8 4.5V8L10.5 9.5" stroke="currentColor" strokeLinecap="round" />
    </svg>
  );
}

function safeCronstrue(expr: string): string {
  try {
    return cronstrue.toString(expr, { verbose: false, use24HourTimeFormat: true });
  } catch {
    return expr;
  }
}
