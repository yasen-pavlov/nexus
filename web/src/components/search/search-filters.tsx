import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Calendar, Check } from "lucide-react";
import { Input } from "@/components/ui/input";
import { SourceChip } from "@/components/source-chip";
import type { Facet } from "@/lib/api-types";
import type { SearchParams } from "@/lib/search-params";
import { hasAnyFilter } from "@/lib/search-params";
import { cn } from "@/lib/utils";

// useNavigate without `from` can't narrow `search` to a route, so TS types
// it as `never`. Cast at the boundary; payload stays typed via SearchParams.
type AnyNavigate = (opts: { search: SearchParams; replace?: boolean }) => void;

interface Props {
  params: SearchParams;
  facets?: Record<string, Facet[]>;
}

type DatePresetKey = "any" | "24h" | "7d" | "30d" | "90d" | "ytd";

const DATE_PRESETS: { key: DatePresetKey; label: string }[] = [
  { key: "any", label: "Any time" },
  { key: "24h", label: "Past 24 hours" },
  { key: "7d", label: "Past 7 days" },
  { key: "30d", label: "Past 30 days" },
  { key: "90d", label: "Past 90 days" },
  { key: "ytd", label: "Year to date" },
];

function isoDaysAgo(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() - days);
  return d.toISOString().slice(0, 10);
}

function activeDateLabel(params: SearchParams): string {
  if (!params.date_from && !params.date_to) return "Any time";
  const from = params.date_from ?? "…";
  const to = params.date_to ?? "now";
  return `${from} → ${to}`;
}

export function SearchFilters({ params, facets }: Readonly<Props>) {
  const navigate = useNavigate() as unknown as AnyNavigate;
  const [dateOpen, setDateOpen] = useState(false);

  const sourceTypeFacets = facets?.source_type ?? [];
  const sourceNameFacets = facets?.source_name ?? [];
  // Only show connector facets when they add information beyond source_type.
  const showConnectorFacets =
    sourceNameFacets.length > sourceTypeFacets.length;

  const activeSources = params.sources ?? [];
  const activeNames = params.source_names ?? [];
  const hasDate = !!params.date_from || !!params.date_to;

  const update = (next: Partial<SearchParams>) =>
    navigate({ search: { ...params, ...next }, replace: true });

  const toggleSource = (value: string) => {
    const next = activeSources.includes(value)
      ? activeSources.filter((v) => v !== value)
      : [...activeSources, value];
    update({ sources: next.length ? next : undefined });
  };

  const toggleName = (value: string) => {
    const next = activeNames.includes(value)
      ? activeNames.filter((v) => v !== value)
      : [...activeNames, value];
    update({ source_names: next.length ? next : undefined });
  };

  const applyPreset = (key: DatePresetKey) => {
    if (key === "any") {
      update({ date_from: undefined, date_to: undefined });
    } else {
      const DAYS_BY_PRESET: Record<Exclude<DatePresetKey, "any">, number> = {
        "24h": 1,
        "7d": 7,
        "30d": 30,
        "90d": 90,
        ytd: 90,
      };
      const days = DAYS_BY_PRESET[key] ?? 90;
      const dateFrom =
        key === "ytd"
          ? `${new Date().getFullYear()}-01-01`
          : isoDaysAgo(days);
      update({
        date_from: dateFrom,
        date_to: undefined,
      });
    }
    setDateOpen(false);
  };

  const setCustomRange = (from: string, to: string) =>
    update({
      date_from: from || undefined,
      date_to: to || undefined,
    });

  const clearAll = () =>
    update({
      sources: undefined,
      source_names: undefined,
      date_from: undefined,
      date_to: undefined,
    });

  if (
    sourceTypeFacets.length === 0 &&
    !showConnectorFacets &&
    !hasAnyFilter(params)
  ) {
    return null;
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      {/* Source-type facets — colored pills. The one place in the app where
          source color chips shine as selectable filters. */}
      {sourceTypeFacets.map((f) => {
        const active = activeSources.includes(f.value);
        return (
          <button
            key={`st-${f.value}`}
            type="button"
            onClick={() => toggleSource(f.value)}
            aria-pressed={active}
            className="appearance-none rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <SourceChip
              type={f.value}
              variant="pill"
              count={f.count}
              active={active}
              className="h-7 cursor-pointer"
            />
          </button>
        );
      })}

      {/* Connector (source_name) facets — neutral pills so they don't
          fight the colored source pills for attention. */}
      {showConnectorFacets &&
        sourceNameFacets.map((f) => {
          const active = activeNames.includes(f.value);
          return (
            <button
              key={`sn-${f.value}`}
              type="button"
              onClick={() => toggleName(f.value)}
              aria-pressed={active}
              className={cn(
                "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-full border px-3 text-xs font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                active
                  ? "border-primary/40 bg-primary/10 text-foreground"
                  : "border-border bg-card text-foreground/80 hover:border-primary/30 hover:bg-card-hover",
              )}
            >
              <span>{f.value}</span>
              <span
                className={cn(
                  "tabular-nums",
                  active
                    ? "text-primary/80"
                    : "text-muted-foreground/70",
                )}
              >
                {f.count}
              </span>
            </button>
          );
        })}

      {/* Date preset button + popover */}
      <div className="relative">
        <button
          type="button"
          onClick={() => setDateOpen((v) => !v)}
          aria-expanded={dateOpen}
          className={cn(
            "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-full border px-3 text-xs font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
            hasDate
              ? "border-primary/40 bg-primary/10 text-foreground"
              : "border-border bg-card text-foreground/80 hover:border-primary/30 hover:bg-card-hover",
          )}
        >
          <Calendar className="size-3.5" aria-hidden />
          <span>{activeDateLabel(params)}</span>
        </button>

        {dateOpen && (
          <>
            <button
              type="button"
              aria-label="Close date picker"
              onClick={() => setDateOpen(false)}
              className="fixed inset-0 z-40 cursor-default"
            />
            <div
              role="dialog"
              className="absolute left-0 top-full z-50 mt-1.5 w-64 max-w-[calc(100vw-1.5rem)] overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-sm"
            >
              <div className="px-3 pb-1.5 pt-2 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/70">
                Date range
              </div>
              <ul className="pb-1">
                {DATE_PRESETS.map((p) => {
                  const isAny = p.key === "any";
                  const isActiveAny = isAny && !hasDate;
                  return (
                    <li key={p.key}>
                      <button
                        type="button"
                        onClick={() => applyPreset(p.key)}
                        className={cn(
                          "flex w-full items-center justify-between px-3 py-1.5 text-[13px] transition-colors",
                          "text-foreground/90 hover:bg-accent hover:text-foreground",
                          isActiveAny && "bg-primary/10 text-foreground",
                        )}
                      >
                        <span>{p.label}</span>
                        {isActiveAny && (
                          <Check
                            className="size-3.5 text-primary"
                            aria-hidden
                          />
                        )}
                      </button>
                    </li>
                  );
                })}
              </ul>
              <div className="border-t border-border/70 px-3 py-2">
                <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/70">
                  Custom range
                </div>
                <div className="flex items-center gap-1.5">
                  <Input
                    type="date"
                    aria-label="From"
                    value={params.date_from ?? ""}
                    onChange={(e) =>
                      setCustomRange(e.target.value, params.date_to ?? "")
                    }
                    className="h-7 flex-1 text-[12px]"
                  />
                  <span aria-hidden className="text-muted-foreground/70">
                    →
                  </span>
                  <Input
                    type="date"
                    aria-label="To"
                    value={params.date_to ?? ""}
                    onChange={(e) =>
                      setCustomRange(params.date_from ?? "", e.target.value)
                    }
                    className="h-7 flex-1 text-[12px]"
                  />
                </div>
              </div>
            </div>
          </>
        )}
      </div>

      {hasAnyFilter(params) && (
        <button
          type="button"
          onClick={clearAll}
          className="ml-1 text-[12px] font-medium text-muted-foreground underline-offset-2 transition-colors hover:text-foreground hover:underline"
        >
          Clear
        </button>
      )}
    </div>
  );
}
