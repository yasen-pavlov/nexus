import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Calendar, Filter, Plus, X } from "lucide-react";
import type { Facet } from "@/lib/api-types";
import type { SearchParams } from "@/lib/search-params";
import { hasAnyFilter } from "@/lib/search-params";
import { cn } from "@/lib/utils";

// Boundary cast: useNavigate without `from` types `search` as never.
type AnyNavigate = (opts: { search: SearchParams; replace?: boolean }) => void;

interface Props {
  params: SearchParams;
  facets?: Record<string, Facet[]>;
}

type DatePreset = {
  label: string;
  key: "any" | "24h" | "7d" | "30d" | "90d" | "ytd" | "custom";
};

const DATE_PRESETS: DatePreset[] = [
  { label: "Any time", key: "any" },
  { label: "Past 24h", key: "24h" },
  { label: "Past 7 days", key: "7d" },
  { label: "Past 30 days", key: "30d" },
  { label: "Past 90 days", key: "90d" },
  { label: "Year to date", key: "ytd" },
  { label: "Custom…", key: "custom" },
];

function isoDaysAgo(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() - days);
  return d.toISOString().slice(0, 10);
}

function detectPreset(params: SearchParams): DatePreset["key"] {
  if (!params.date_from && !params.date_to) return "any";
  // Anything with a set date is treated as a custom range; typing it as
  // a preset would lie to the user.
  return "custom";
}

function presetLabel(params: SearchParams): string {
  const key = detectPreset(params);
  if (key === "any") return "any time";
  if (key === "custom") {
    const f = params.date_from ?? "…";
    const t = params.date_to ?? "now";
    return `${f} → ${t}`;
  }
  return DATE_PRESETS.find((p) => p.key === key)?.label.toLowerCase() ?? "any";
}

export function SearchFilters({ params, facets }: Props) {
  const navigate = useNavigate() as unknown as AnyNavigate;
  const [adding, setAdding] = useState<"source" | "connector" | "date" | null>(
    null,
  );

  const sourceTypeFacets = facets?.source_type ?? [];
  const sourceNameFacets = facets?.source_name ?? [];
  const showConnectorFacets =
    sourceNameFacets.length > sourceTypeFacets.length;

  const update = (next: Partial<SearchParams>) =>
    navigate({ search: { ...params, ...next }, replace: true });

  const removeSource = (value: string) => {
    const remaining = (params.sources ?? []).filter((v) => v !== value);
    update({ sources: remaining.length ? remaining : undefined });
  };
  const addSource = (value: string) => {
    const cur = params.sources ?? [];
    if (!cur.includes(value)) update({ sources: [...cur, value] });
    setAdding(null);
  };
  const removeName = (value: string) => {
    const remaining = (params.source_names ?? []).filter((v) => v !== value);
    update({ source_names: remaining.length ? remaining : undefined });
  };
  const addName = (value: string) => {
    const cur = params.source_names ?? [];
    if (!cur.includes(value)) update({ source_names: [...cur, value] });
    setAdding(null);
  };

  const applyPreset = (key: DatePreset["key"]) => {
    if (key === "any") {
      update({ date_from: undefined, date_to: undefined });
    } else if (key === "custom") {
      // Leave date fields; user interacts with the inline range below.
      return;
    } else {
      const days =
        key === "24h" ? 1 : key === "7d" ? 7 : key === "30d" ? 30 : 90;
      update({
        date_from:
          key === "ytd"
            ? `${new Date().getFullYear()}-01-01`
            : isoDaysAgo(days),
        date_to: undefined,
      });
    }
    setAdding(null);
  };

  const clearAll = () =>
    update({
      sources: undefined,
      source_names: undefined,
      date_from: undefined,
      date_to: undefined,
    });

  const activeSources = params.sources ?? [];
  const activeNames = params.source_names ?? [];
  const hasDate = !!params.date_from || !!params.date_to;
  const hasAny = hasAnyFilter(params);

  return (
    <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1.5 border-b border-border/60 py-2 font-mono text-xs">
      <span className="inline-flex items-center gap-1 text-muted-foreground/70">
        <Filter className="size-3" aria-hidden />
        <span className="uppercase tracking-wider">filters</span>
      </span>

      {!hasAny && (
        <span className="relative text-muted-foreground/60">
          none
          <span className="mx-1 text-muted-foreground/40">·</span>
          <button
            type="button"
            onClick={() => setAdding(adding ? null : "source")}
            className="text-foreground/90 underline-offset-2 hover:underline"
          >
            add
          </button>
          {adding && (
            <AddMenu
              mode={adding}
              setMode={setAdding}
              sourceTypeFacets={sourceTypeFacets}
              sourceNameFacets={sourceNameFacets}
              showConnectorFacets={showConnectorFacets}
              activeSources={activeSources}
              activeNames={activeNames}
              onAddSource={addSource}
              onAddName={addName}
              onPickPreset={applyPreset}
              onCustomDate={(from, to) =>
                update({ date_from: from, date_to: to })
              }
              currentDateFrom={params.date_from ?? ""}
              currentDateTo={params.date_to ?? ""}
              onClose={() => setAdding(null)}
            />
          )}
        </span>
      )}

      {activeSources.map((v) => {
        const count = sourceTypeFacets.find((f) => f.value === v)?.count;
        return (
          <Token
            key={`s-${v}`}
            label="source"
            value={v}
            count={count}
            onRemove={() => removeSource(v)}
          />
        );
      })}
      {activeNames.map((v) => {
        const count = sourceNameFacets.find((f) => f.value === v)?.count;
        return (
          <Token
            key={`n-${v}`}
            label="in"
            value={v}
            count={count}
            onRemove={() => removeName(v)}
          />
        );
      })}
      {hasDate && (
        <Token
          label="date"
          value={presetLabel(params)}
          onRemove={() =>
            update({ date_from: undefined, date_to: undefined })
          }
        />
      )}

      {hasAny && (
        <div className="relative">
          <button
            type="button"
            onClick={() => setAdding(adding ? null : "source")}
            className={cn(
              "inline-flex h-6 items-center gap-1 rounded-sm border border-dashed border-border px-1.5 text-[11px] text-muted-foreground transition-colors hover:border-foreground/40 hover:text-foreground",
              adding && "border-foreground/40 text-foreground",
            )}
          >
            <Plus className="size-3" aria-hidden />
            narrow
          </button>
          {adding && (
            <AddMenu
              mode={adding}
              setMode={setAdding}
              sourceTypeFacets={sourceTypeFacets}
              sourceNameFacets={sourceNameFacets}
              showConnectorFacets={showConnectorFacets}
              activeSources={activeSources}
              activeNames={activeNames}
              onAddSource={addSource}
              onAddName={addName}
              onPickPreset={applyPreset}
              onCustomDate={(from, to) =>
                update({ date_from: from, date_to: to })
              }
              currentDateFrom={params.date_from ?? ""}
              currentDateTo={params.date_to ?? ""}
              onClose={() => setAdding(null)}
            />
          )}
        </div>
      )}

      {hasAny && (
        <>
          <span className="mx-1 text-muted-foreground/40">·</span>
          <button
            type="button"
            onClick={clearAll}
            className="text-muted-foreground/80 underline-offset-2 hover:text-foreground hover:underline"
          >
            clear
          </button>
        </>
      )}
    </div>
  );
}

// Token — a filter expressed as `key:value` with an integrated × to remove.
// Mono, tight, no round pill. Counts trail right-aligned in dim tabular-nums.
function Token({
  label,
  value,
  count,
  onRemove,
}: {
  label: string;
  value: string;
  count?: number;
  onRemove: () => void;
}) {
  return (
    <span className="group inline-flex h-6 items-center gap-1 rounded-sm bg-foreground px-1.5 text-[11px] text-background">
      <span className="text-background/60">{label}</span>
      <span className="text-background/40">:</span>
      <span className="font-medium">{value}</span>
      {count !== undefined && (
        <span className="tabular-nums text-background/50">{count}</span>
      )}
      <button
        type="button"
        onClick={onRemove}
        aria-label={`remove ${label}:${value}`}
        className="-mr-0.5 ml-0.5 flex size-3.5 items-center justify-center rounded-xs text-background/60 transition-colors hover:bg-background/15 hover:text-background"
      >
        <X className="size-2.5" aria-hidden />
      </button>
    </span>
  );
}

// AddMenu — inline popover with three tabs: source / connector / date.
// Kept minimal: a vertical list of clickable rows, not a form.
function AddMenu({
  mode,
  setMode,
  sourceTypeFacets,
  sourceNameFacets,
  showConnectorFacets,
  activeSources,
  activeNames,
  onAddSource,
  onAddName,
  onPickPreset,
  onCustomDate,
  currentDateFrom,
  currentDateTo,
  onClose,
}: {
  mode: "source" | "connector" | "date";
  setMode: (m: "source" | "connector" | "date") => void;
  sourceTypeFacets: Facet[];
  sourceNameFacets: Facet[];
  showConnectorFacets: boolean;
  activeSources: string[];
  activeNames: string[];
  onAddSource: (v: string) => void;
  onAddName: (v: string) => void;
  onPickPreset: (k: DatePreset["key"]) => void;
  onCustomDate: (from: string, to: string) => void;
  currentDateFrom: string;
  currentDateTo: string;
  onClose: () => void;
}) {
  const tabs: Array<{ key: "source" | "connector" | "date"; label: string }> = [
    { key: "source", label: "source" },
    ...(showConnectorFacets
      ? [{ key: "connector" as const, label: "in" }]
      : []),
    { key: "date", label: "date" },
  ];

  return (
    <>
      <button
        type="button"
        aria-label="close filter menu"
        onClick={onClose}
        className="fixed inset-0 z-40 cursor-default"
      />
      <div
        role="dialog"
        className="absolute left-0 top-full z-50 mt-1 w-64 overflow-hidden border border-border bg-popover font-mono text-xs text-popover-foreground shadow-sm"
      >
        <div className="flex items-center gap-px border-b border-border bg-muted/40">
          {tabs.map((t) => (
            <button
              key={t.key}
              type="button"
              onClick={() => setMode(t.key)}
              className={cn(
                "flex-1 px-2 py-1.5 text-center text-[11px] transition-colors",
                mode === t.key
                  ? "bg-background text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {t.label}
            </button>
          ))}
        </div>

        <div className="max-h-64 overflow-y-auto py-1">
          {mode === "source" && (
            <FacetList
              facets={sourceTypeFacets}
              active={activeSources}
              onPick={onAddSource}
              empty="no source facets"
            />
          )}
          {mode === "connector" && (
            <FacetList
              facets={sourceNameFacets}
              active={activeNames}
              onPick={onAddName}
              empty="no connector facets"
            />
          )}
          {mode === "date" && (
            <div>
              {DATE_PRESETS.filter((p) => p.key !== "custom").map((p) => (
                <button
                  key={p.key}
                  type="button"
                  onClick={() => onPickPreset(p.key)}
                  className="flex w-full items-center gap-2 px-2 py-1 text-left hover:bg-accent hover:text-accent-foreground"
                >
                  <Calendar
                    className="size-3 text-muted-foreground"
                    aria-hidden
                  />
                  {p.label}
                </button>
              ))}
              <div className="mt-1 border-t border-border/60 pt-1">
                <div className="px-2 py-1 text-[10px] uppercase tracking-wider text-muted-foreground/60">
                  custom range
                </div>
                <div className="flex items-center gap-1 px-2 pb-1.5">
                  <input
                    type="date"
                    aria-label="from"
                    className="h-6 w-[46%] rounded-sm border border-input bg-background px-1 text-[11px] outline-none focus-visible:border-ring"
                    value={currentDateFrom}
                    onChange={(e) =>
                      onCustomDate(e.target.value, currentDateTo)
                    }
                  />
                  <span className="text-muted-foreground/60">→</span>
                  <input
                    type="date"
                    aria-label="to"
                    className="h-6 w-[46%] rounded-sm border border-input bg-background px-1 text-[11px] outline-none focus-visible:border-ring"
                    value={currentDateTo}
                    onChange={(e) =>
                      onCustomDate(currentDateFrom, e.target.value)
                    }
                  />
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    </>
  );
}

function FacetList({
  facets,
  active,
  onPick,
  empty,
}: {
  facets: Facet[];
  active: string[];
  onPick: (value: string) => void;
  empty: string;
}) {
  if (facets.length === 0) {
    return <div className="px-2 py-3 text-muted-foreground/70">{empty}</div>;
  }
  return (
    <>
      {facets.map((f) => {
        const isActive = active.includes(f.value);
        return (
          <button
            key={f.value}
            type="button"
            disabled={isActive}
            onClick={() => onPick(f.value)}
            className={cn(
              "flex w-full items-center justify-between gap-3 px-2 py-1 text-left transition-colors",
              isActive
                ? "cursor-not-allowed text-muted-foreground/50"
                : "hover:bg-accent hover:text-accent-foreground",
            )}
          >
            <span className="truncate">
              {f.value}
              {isActive && (
                <span className="ml-1 text-muted-foreground/50">·active</span>
              )}
            </span>
            <span className="tabular-nums text-muted-foreground/60">
              {f.count}
            </span>
          </button>
        );
      })}
    </>
  );
}
