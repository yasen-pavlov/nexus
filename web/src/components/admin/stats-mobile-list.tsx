import { Link } from "@tanstack/react-router";
import { ArrowUpRight } from "lucide-react";

import { ConnectorLogo } from "@/components/connectors/connector-logo";
import { SourceChip } from "@/components/source-chip";
import {
  formatAbsolute,
  formatBytes,
  formatCount,
  formatRelative,
} from "@/lib/format";

import type { AdminPerSourceStats } from "@/lib/api-types";

export interface StatsMobileListProps {
  rows: AdminPerSourceStats[];
}

/**
 * Mobile card-stack variant of the per-source stats table. Each source
 * gets a card with the connectors-page tonal spine, brass logo plate,
 * and a 2-col stat grid below the heading.
 */
export function StatsMobileList({ rows }: Readonly<StatsMobileListProps>) {
  return (
    <div className="flex flex-col gap-2">
      {rows.map((row) => (
        <article
          key={`${row.source_type}:${row.source_name}`}
          className="relative overflow-hidden rounded-lg border border-border bg-card p-3"
        >
          {/* Tonal spine — matches ConnectorCard. */}
          <span
            aria-hidden
            className="absolute left-0 top-2 bottom-2 w-[3px] rounded-full"
            style={
              {
                backgroundColor: `color-mix(in oklch, var(--source-${row.source_type}, var(--source-default)) 55%, transparent)`,
              } as React.CSSProperties
            }
          />

          <header className="flex items-center gap-3 pl-1">
            <ConnectorLogo type={row.source_type} size="sm" />
            <div className="min-w-0 flex-1 leading-tight">
              <div className="truncate text-[14.5px] font-medium text-foreground">
                {row.source_name}
              </div>
              <div className="mt-1">
                <SourceChip type={row.source_type} />
              </div>
            </div>
            <Link
              to="/connectors"
              aria-label={`View connectors filtered by ${row.source_name}`}
              className="-mr-1 inline-flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground/70 transition-colors hover:bg-accent hover:text-foreground"
            >
              <ArrowUpRight className="size-3.5" aria-hidden />
            </Link>
          </header>

          <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2.5 pl-1">
            <Stat
              label="Documents"
              value={
                <>
                  <span className="tabular-nums text-foreground">
                    {formatCount(row.document_count)}
                  </span>{" "}
                  <span className="text-[11.5px] tabular-nums text-muted-foreground">
                    · {formatCount(row.chunk_count)} chunks
                  </span>
                </>
              }
            />
            <Stat
              label="Cache"
              value={
                <>
                  <span className="tabular-nums text-foreground">
                    {formatBytes(row.cache_bytes)}
                  </span>
                  {row.cache_count > 0 && (
                    <>
                      {" "}
                      <span className="text-[11.5px] tabular-nums text-muted-foreground">
                        · {formatCount(row.cache_count)}
                      </span>
                    </>
                  )}
                </>
              }
            />
            <Stat
              label="Latest"
              value={
                <span
                  title={formatAbsolute(row.latest_indexed_at)}
                  className="tabular-nums text-foreground"
                >
                  {formatRelative(row.latest_indexed_at)}
                </span>
              }
              wide
            />
          </div>
        </article>
      ))}
    </div>
  );
}

function Stat({
  label,
  value,
  wide = false,
}: Readonly<{
  label: string;
  value: React.ReactNode;
  wide?: boolean;
}>) {
  return (
    <div className={wide ? "col-span-2" : undefined}>
      <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/70">
        {label}
      </div>
      <div className="mt-0.5 text-[13px] leading-tight">{value}</div>
    </div>
  );
}
