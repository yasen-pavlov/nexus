import { createFileRoute, Link } from "@tanstack/react-router";
import { useMemo } from "react";
import {
  Activity,
  AlertCircle,
  ArrowUpRight,
  Brain,
  FolderArchive,
  Scale,
  Users as UsersIcon,
} from "lucide-react";
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";

import { Skeleton } from "@/components/ui/skeleton";
import { SourceChip } from "@/components/source-chip";
import { ConnectorLogo } from "@/components/connectors/connector-logo";

import { SettingsSection } from "@/components/admin/settings-section";
import { StatsMobileList } from "@/components/admin/stats-mobile-list";

import { useIsMobile } from "@/hooks/use-mobile";
import { useSystemStats } from "@/hooks/use-system-stats";
import type { AdminPerSourceStats, AdminStats } from "@/lib/api-types";
import {
  formatAbsolute,
  formatBytes,
  formatCount,
  formatRelative,
} from "@/lib/format";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_authenticated/admin/stats")({
  component: StatsPage,
});

function StatsPage() {
  const { data, isPending, error } = useSystemStats();
  const isMobile = useIsMobile();

  return (
    <div className="mx-auto w-full max-w-5xl flex-1 px-6 py-8">
      <header className="mb-8">
        <h1 className="text-[20px] font-medium tracking-[-0.005em] text-foreground">
          Stats
        </h1>
        <p className="mt-1 text-[13.5px] leading-[1.55] text-muted-foreground">
          What Nexus has indexed, how fresh it is, and what&apos;s powering
          retrieval right now.
        </p>
      </header>

      {error ? (
        <ErrorPlaque message={error.message} />
      ) : (
        <div className="flex flex-col gap-10">
          <KpiStrip data={data} isPending={isPending} />

          <SettingsSection
            id="per-source"
            label="Sources · 01"
            title="Per source"
            icon={Activity}
            description="Document counts, cache footprint, and recency per connector instance."
          >
            {isPending ? (
              <PerSourceSkeleton />
            ) : !data || data.per_source.length === 0 ? (
              <PerSourceEmpty />
            ) : isMobile ? (
              <StatsMobileList rows={data.per_source} />
            ) : (
              <PerSourceTable rows={data.per_source} />
            )}
          </SettingsSection>

          <SettingsSection
            id="engines"
            label="Engines · 02"
            title="Retrieval stack"
            icon={Brain}
            description="The providers currently powering semantic search and reranking."
          >
            {isPending ? (
              <EnginePanelSkeleton />
            ) : (
              <EnginePanel stats={data ?? undefined} />
            )}
          </SettingsSection>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// KPI strip
// ---------------------------------------------------------------------------

function KpiStrip({
  data,
  isPending,
}: {
  data?: AdminStats;
  isPending: boolean;
}) {
  if (isPending) {
    return (
      <div className="grid gap-3 sm:grid-cols-3">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-[124px] w-full rounded-lg" />
        ))}
      </div>
    );
  }

  const stats = data ?? {
    total_documents: 0,
    total_chunks: 0,
    users_count: 0,
    per_source: [],
  };

  const latest = stats.per_source
    .map((s) => s.latest_indexed_at)
    .filter((t): t is string => !!t)
    .sort((a, b) => (a < b ? 1 : -1))[0];

  const totalCacheBytes = stats.per_source.reduce(
    (sum, s) => sum + s.cache_bytes,
    0,
  );

  const userLabel = `${formatCount(stats.users_count)} user${stats.users_count === 1 ? "" : "s"}`;

  return (
    <div className="grid gap-3 sm:grid-cols-3">
      <KpiPlaque
        eyebrow="Indexed"
        icon={FolderArchive}
        value={
          <span className="text-[32px] font-medium tracking-[-0.01em] tabular-nums">
            {formatCount(stats.total_documents)}
          </span>
        }
        caption={`${formatCount(stats.total_chunks)} chunks · ${userLabel}`}
      />
      <KpiPlaque
        eyebrow="Connected"
        icon={Activity}
        value={
          <span className="text-[32px] font-medium tracking-[-0.01em] tabular-nums">
            {stats.per_source.length}
          </span>
        }
        caption={
          stats.per_source.length > 0
            ? `${formatBytes(totalCacheBytes)} across ${stats.per_source.length} source${stats.per_source.length === 1 ? "" : "s"}`
            : "No sources yet"
        }
      />
      <KpiPlaque
        eyebrow="Latest activity"
        icon={UsersIcon}
        value={
          latest ? (
            <span
              title={formatAbsolute(latest)}
              className="text-[20px] font-medium tracking-[-0.005em]"
            >
              {formatRelative(latest)}
            </span>
          ) : (
            <span className="text-[32px] font-medium tracking-[-0.01em] text-muted-foreground/60">
              —
            </span>
          )
        }
        caption={latest ? formatAbsolute(latest) : "No content indexed yet"}
      />
    </div>
  );
}

function KpiPlaque({
  eyebrow,
  icon: Icon,
  value,
  caption,
}: {
  eyebrow: string;
  icon: typeof Activity;
  value: React.ReactNode;
  caption: string;
}) {
  return (
    <div className="relative overflow-hidden rounded-lg border border-border bg-card p-5">
      <Icon
        aria-hidden
        className="pointer-events-none absolute -right-3 -top-3 size-20 text-foreground/[0.04]"
        strokeWidth={1.5}
      />
      <div className="relative flex min-h-full flex-col gap-3">
        <div className="flex items-center gap-2.5">
          <span
            aria-hidden
            className="h-[2px] w-6 rounded-full bg-primary/35"
          />
          <span className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
            {eyebrow}
          </span>
        </div>
        <div className="leading-none text-foreground">{value}</div>
        <div className="text-[12px] leading-[1.4] text-muted-foreground">
          {caption}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Per-source table
// ---------------------------------------------------------------------------

const columnHelper = createColumnHelper<AdminPerSourceStats>();

function PerSourceTable({ rows }: { rows: AdminPerSourceStats[] }) {
  const columns = useMemo(
    () => [
      columnHelper.accessor("source_name", {
        header: () => <span>Source</span>,
        cell: (info) => {
          const row = info.row.original;
          return (
            <div className="flex min-w-0 items-center gap-3">
              <ConnectorLogo type={row.source_type} size="sm" />
              <div className="min-w-0 leading-tight">
                <div className="truncate text-[13.5px] font-medium text-foreground">
                  {row.source_name}
                </div>
                <div className="mt-0.5">
                  <SourceChip type={row.source_type} />
                </div>
              </div>
            </div>
          );
        },
      }),
      columnHelper.accessor("document_count", {
        header: () => <span>Documents</span>,
        cell: (info) => {
          const row = info.row.original;
          return (
            <div className="leading-tight">
              <div className="text-[13.5px] tabular-nums text-foreground">
                {formatCount(row.document_count)}
              </div>
              <div className="mt-0.5 text-[11.5px] tabular-nums text-muted-foreground">
                {formatCount(row.chunk_count)} chunks
              </div>
            </div>
          );
        },
      }),
      columnHelper.accessor((row) => row.latest_indexed_at ?? "", {
        id: "fresh",
        header: () => <span>Fresh</span>,
        cell: (info) => {
          const iso = info.row.original.latest_indexed_at;
          return (
            <div
              title={formatAbsolute(iso)}
              className="text-[13px] tabular-nums text-muted-foreground"
            >
              {formatRelative(iso)}
            </div>
          );
        },
      }),
      columnHelper.accessor("cache_bytes", {
        header: () => <span className="block sm:text-right">Cache</span>,
        cell: (info) => {
          const row = info.row.original;
          return (
            <div className="flex items-baseline gap-1.5 sm:justify-end">
              <span className="text-[13.5px] tabular-nums text-foreground">
                {formatBytes(row.cache_bytes)}
              </span>
              {row.cache_count > 0 && (
                <span className="text-[11.5px] tabular-nums text-muted-foreground">
                  {formatCount(row.cache_count)}
                </span>
              )}
            </div>
          );
        },
      }),
      columnHelper.display({
        id: "actions",
        header: () => <span className="sr-only">Open</span>,
        cell: (info) => {
          const row = info.row.original;
          return (
            <div className="flex sm:justify-end">
              <Link
                to="/connectors"
                aria-label={`View connectors filtered by ${row.source_name}`}
                className="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground/70 transition-colors hover:bg-accent hover:text-foreground"
              >
                <ArrowUpRight className="size-3.5" aria-hidden />
              </Link>
            </div>
          );
        },
      }),
    ],
    [],
  );

  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  const gridCols =
    "sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto]";

  return (
    <div className="flex flex-col">
      <div
        className={cn(
          "hidden border-b border-border/60 px-3 pb-2 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80 sm:grid sm:items-center sm:gap-4",
          gridCols,
        )}
      >
        {table.getHeaderGroups()[0].headers.map((header) => (
          <div key={header.id}>
            {flexRender(header.column.columnDef.header, header.getContext())}
          </div>
        ))}
      </div>

      <div className="divide-y divide-border/60">
        {table.getRowModel().rows.map((row) => {
          const sourceType = row.original.source_type;
          return (
            <div
              key={row.id}
              className={cn(
                "relative grid grid-cols-1 gap-2 rounded-md px-3 py-3 transition-colors hover:bg-card-hover sm:items-center sm:gap-4",
                gridCols,
              )}
            >
              <span
                aria-hidden
                className="absolute left-0 top-2 bottom-2 w-[2px] rounded-full"
                style={
                  {
                    backgroundColor: `color-mix(in oklch, var(--source-${sourceType}, var(--source-default)) 55%, transparent)`,
                  } as React.CSSProperties
                }
              />
              {row.getVisibleCells().map((cell) => (
                <div key={cell.id} className="min-w-0">
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </div>
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function PerSourceSkeleton() {
  return (
    <div className="flex flex-col gap-2">
      {[0, 1, 2].map((i) => (
        <Skeleton key={i} className="h-14 w-full rounded-md" />
      ))}
    </div>
  );
}

function PerSourceEmpty() {
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <div className="flex size-11 items-center justify-center rounded-xl bg-primary/15 text-primary">
        <FolderArchive className="size-5" aria-hidden />
      </div>
      <div>
        <div className="text-[14px] font-medium text-foreground">
          No sources indexed yet
        </div>
        <div className="text-[12.5px] text-muted-foreground">
          Connect a source to see document counts and freshness here.
        </div>
      </div>
      <Link
        to="/connectors"
        className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-[13px] font-medium text-primary-foreground transition-colors hover:bg-primary/90"
      >
        Add a connector
        <ArrowUpRight className="size-3.5" aria-hidden />
      </Link>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Engine panel
// ---------------------------------------------------------------------------

function EnginePanel({ stats }: { stats?: AdminStats }) {
  if (!stats) return null;
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <EngineCard
        title="Embeddings"
        icon={Brain}
        kind="embeddings"
        enabled={stats.embedding.enabled}
        provider={stats.embedding.provider}
        model={stats.embedding.model}
        dimension={stats.embedding.dimension}
      />
      <EngineCard
        title="Reranking"
        icon={Scale}
        kind="rerank"
        enabled={stats.rerank.enabled}
        provider={stats.rerank.provider}
        model={stats.rerank.model}
      />
    </div>
  );
}

function EngineCard({
  title,
  icon: Icon,
  kind,
  enabled,
  provider,
  model,
  dimension,
}: {
  title: string;
  icon: typeof Brain;
  kind: "embeddings" | "rerank";
  enabled: boolean;
  provider: string;
  model: string;
  dimension?: number;
}) {
  return (
    <div className="flex flex-col gap-4 rounded-lg border border-border bg-card p-5">
      <header className="flex items-center gap-3">
        <div
          aria-hidden
          className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/15 text-primary"
        >
          <Icon className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-[15px] font-medium text-foreground">
            {title}
          </div>
        </div>
        <StatusPill enabled={enabled} />
      </header>

      <div className="flex-1">
        {enabled ? (
          <div className="flex flex-col gap-2">
            <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
              {provider || "—"}
            </div>
            <div className="flex items-center gap-2">
              <span className="truncate font-mono text-[14px] text-foreground">
                {model || "—"}
              </span>
              {dimension !== undefined && dimension > 0 && (
                <span className="rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium tabular-nums text-muted-foreground/90">
                  {dimension}d
                </span>
              )}
            </div>
          </div>
        ) : (
          <div className="rounded-md border border-dashed border-border/70 bg-background/40 px-3 py-3 text-[12.5px] text-muted-foreground/80">
            {title} disabled.{" "}
            <Link
              to="/admin/settings"
              hash={kind === "embeddings" ? "embeddings" : "rerank"}
              className="font-medium text-primary hover:underline"
            >
              Configure →
            </Link>
          </div>
        )}
      </div>

      <footer className="flex justify-end">
        <Link
          to="/admin/settings"
          hash={kind === "embeddings" ? "embeddings" : "rerank"}
          className="inline-flex items-center gap-1 text-[12px] text-muted-foreground transition-colors hover:text-foreground"
        >
          Change
          <ArrowUpRight className="size-3" aria-hidden />
        </Link>
      </footer>
    </div>
  );
}

function StatusPill({ enabled }: { enabled: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium uppercase tracking-[0.08em]",
        enabled
          ? "bg-primary/10 text-primary"
          : "bg-muted text-muted-foreground",
      )}
    >
      <span
        aria-hidden
        className={cn(
          "size-1.5 rounded-full",
          enabled ? "bg-primary" : "bg-muted-foreground/50",
        )}
      />
      {enabled ? "Active" : "Disabled"}
    </span>
  );
}

function EnginePanelSkeleton() {
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Skeleton className="h-40 w-full rounded-lg" />
      <Skeleton className="h-40 w-full rounded-lg" />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Error
// ---------------------------------------------------------------------------

function ErrorPlaque({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4 text-[13px]">
      <AlertCircle
        className="mt-0.5 size-4 shrink-0 text-destructive"
        aria-hidden
      />
      <div className="flex-1 leading-[1.55]">
        <div className="font-medium text-foreground">
          Couldn&apos;t load stats
        </div>
        <div className="text-muted-foreground">{message}</div>
      </div>
    </div>
  );
}
