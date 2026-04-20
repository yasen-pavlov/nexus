import { useMemo, useState } from "react";
import { RotateCcw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Slider } from "@/components/ui/slider";
import { ConnectorLogo } from "@/components/connectors/connector-logo";
import { SourceChip } from "@/components/source-chip";

import {
  useRankingSettings,
  type UseRankingSettings,
} from "@/hooks/use-ranking-settings";
import type { RankingSettings } from "@/lib/api-types";
import { cn } from "@/lib/utils";

// Curated defaults — mirrors search.DefaultRankingConfig() in Go. Used to
// reset per-source cards, detect "dirty from defaults" for the inline
// reset link, and arm the Balanced preset.
const DEFAULTS: {
  halfLife: Record<string, number>;
  floor: Record<string, number>;
  trust: Record<string, number>;
} = {
  halfLife: { telegram: 14, imap: 30, filesystem: 90, paperless: 180 },
  floor: { telegram: 0.65, imap: 0.75, filesystem: 0.85, paperless: 0.9 },
  trust: { telegram: 0.92, imap: 0.92, filesystem: 1, paperless: 1.05 },
};

// Presets per source — "Fresh" tilts toward recent-docs-win, "Archive"
// tilts toward never-decays. Values are deliberately hand-tuned per source
// rather than a single global curve because what counts as "archive" for
// paperless (180d stays 100%) would be nonsense for telegram (chat is
// ephemeral by nature).
const PRESETS: Record<string, Record<string, SourceKnobs>> = {
  telegram: {
    Balanced: { halfLife: 14, floor: 0.65, trust: 0.92 },
    Fresh: { halfLife: 3, floor: 0.4, trust: 0.92 },
    Archive: { halfLife: 60, floor: 0.9, trust: 1 },
  },
  imap: {
    Balanced: { halfLife: 30, floor: 0.75, trust: 0.92 },
    Fresh: { halfLife: 7, floor: 0.5, trust: 0.92 },
    Archive: { halfLife: 180, floor: 0.95, trust: 1.05 },
  },
  filesystem: {
    Balanced: { halfLife: 90, floor: 0.85, trust: 1 },
    Fresh: { halfLife: 14, floor: 0.6, trust: 1 },
    Archive: { halfLife: 365, floor: 0.95, trust: 1.1 },
  },
  paperless: {
    Balanced: { halfLife: 180, floor: 0.9, trust: 1.05 },
    Fresh: { halfLife: 30, floor: 0.7, trust: 1 },
    Archive: { halfLife: 365, floor: 0.98, trust: 1.15 },
  },
};

interface SourceKnobs {
  halfLife: number;
  floor: number;
  trust: number;
}

const SOURCE_ORDER = ["imap", "telegram", "paperless", "filesystem"] as const;
const SOURCE_LABEL: Record<(typeof SOURCE_ORDER)[number], string> = {
  imap: "Email (IMAP)",
  telegram: "Telegram",
  paperless: "Paperless",
  filesystem: "Filesystem",
};

export function RankingForm() {
  const ctx = useRankingSettings();

  if (ctx.isPending || !ctx.data) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-64 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  // Remount on every new server-side snapshot so form state re-seeds from
  // data via useState's initializer — no useEffect-driven sync, no ref
  // reads during render.
  return <RankingFormInner key={rankingFingerprint(ctx.data)} ctx={ctx} />;
}

function rankingFingerprint(s: RankingSettings): string {
  const bySource = (m: Record<string, number>) =>
    SOURCE_ORDER.map((k) => `${k}:${m[k] ?? "?"}`).join(",");
  return [
    bySource(s.source_half_life_days),
    bySource(s.source_recency_floor),
    bySource(s.source_trust_weight),
    s.metadata_bonus_enabled ? "1" : "0",
    s.source_trust_enabled ? "1" : "0",
  ].join("|");
}

function RankingFormInner({ ctx }: { ctx: UseRankingSettings }) {
  const { data, update } = ctx;
  const saved = data!;
  const [form, setForm] = useState<RankingSettings>(saved);

  const dirty = !sameRanking(form, saved);

  const revert = () => setForm(saved);

  const resetAll = () => {
    setForm((f) => ({
      ...f,
      source_half_life_days: { ...DEFAULTS.halfLife },
      source_recency_floor: { ...DEFAULTS.floor },
      source_trust_weight: { ...DEFAULTS.trust },
      metadata_bonus_enabled: true,
      source_trust_enabled: true,
    }));
  };

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        update.mutate(form);
      }}
    >
      {/* Signals card — two global switches */}
      <SignalsCard
        trustEnabled={form.source_trust_enabled}
        metadataEnabled={form.metadata_bonus_enabled}
        onTrustChange={(v) =>
          setForm((f) => ({ ...f, source_trust_enabled: v }))
        }
        onMetadataChange={(v) =>
          setForm((f) => ({ ...f, metadata_bonus_enabled: v }))
        }
      />

      {/* Per-source cards */}
      <div className="flex flex-col gap-4">
        {SOURCE_ORDER.map((src) => {
          const knobs: SourceKnobs = {
            halfLife: form.source_half_life_days[src] ?? DEFAULTS.halfLife[src],
            floor: form.source_recency_floor[src] ?? DEFAULTS.floor[src],
            trust: form.source_trust_weight[src] ?? DEFAULTS.trust[src],
          };
          const dirtyFromDefaults = !sameKnobs(knobs, {
            halfLife: DEFAULTS.halfLife[src],
            floor: DEFAULTS.floor[src],
            trust: DEFAULTS.trust[src],
          });
          return (
            <SourceCard
              key={src}
              sourceType={src}
              label={SOURCE_LABEL[src]}
              knobs={knobs}
              trustDisabled={!form.source_trust_enabled}
              dirtyFromDefaults={dirtyFromDefaults}
              onChange={(next) =>
                setForm((f) => ({
                  ...f,
                  source_half_life_days: {
                    ...f.source_half_life_days,
                    [src]: next.halfLife,
                  },
                  source_recency_floor: {
                    ...f.source_recency_floor,
                    [src]: next.floor,
                  },
                  source_trust_weight: {
                    ...f.source_trust_weight,
                    [src]: next.trust,
                  },
                }))
              }
              onReset={() =>
                setForm((f) => ({
                  ...f,
                  source_half_life_days: {
                    ...f.source_half_life_days,
                    [src]: DEFAULTS.halfLife[src],
                  },
                  source_recency_floor: {
                    ...f.source_recency_floor,
                    [src]: DEFAULTS.floor[src],
                  },
                  source_trust_weight: {
                    ...f.source_trust_weight,
                    [src]: DEFAULTS.trust[src],
                  },
                }))
              }
            />
          );
        })}
      </div>

      {/* Section footer: reset-all + explanatory line */}
      <div className="flex items-center justify-between gap-3 border-t border-border/60 pt-4">
        <p className="text-[12px] text-muted-foreground">
          Changes apply on the next query. No re-index required.
        </p>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={resetAll}
          className="gap-1.5 text-muted-foreground hover:text-foreground"
        >
          <RotateCcw className="size-3.5" aria-hidden />
          Reset all to defaults
        </Button>
      </div>

      {dirty && (
        <div className="sticky bottom-0 -mx-6 -mb-6 mt-2 flex items-center justify-between gap-3 border-t border-border/70 bg-card/95 px-6 py-3 backdrop-blur">
          <div className="flex items-center gap-2 text-[12px] text-muted-foreground">
            <span
              aria-hidden
              className="size-1.5 shrink-0 rounded-full bg-primary"
            />
            <span>Draft · not saved yet</span>
          </div>
          <div className="flex gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={revert}
              disabled={update.isPending}
            >
              Revert
            </Button>
            <Button type="submit" size="sm" disabled={update.isPending}>
              {update.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        </div>
      )}
    </form>
  );
}

// ---------------------------------------------------------------------------
// Signals card
// ---------------------------------------------------------------------------

function SignalsCard({
  trustEnabled,
  metadataEnabled,
  onTrustChange,
  onMetadataChange,
}: {
  trustEnabled: boolean;
  metadataEnabled: boolean;
  onTrustChange: (v: boolean) => void;
  onMetadataChange: (v: boolean) => void;
}) {
  return (
    <div className="rounded-md border border-border/70 bg-background/40 p-4">
      <div className="mb-3">
        <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
          Signals
        </div>
        <div className="mt-0.5 text-[12px] leading-[1.5] text-muted-foreground">
          Global switches. Per-source knobs below are gated by these.
        </div>
      </div>

      <div className="flex flex-col divide-y divide-border/50">
        <ToggleRow
          label="Apply source trust weights"
          description="Multiply each source's rerank score by its per-source trust weight. Disable to treat all sources equally."
          active={trustEnabled}
          onChange={onTrustChange}
        />
        <ToggleRow
          label="Apply metadata bonus"
          description="Boost results whose structured metadata (filename, sender, tags) matches the query."
          active={metadataEnabled}
          onChange={onMetadataChange}
        />
      </div>
    </div>
  );
}

function ToggleRow({
  label,
  description,
  active,
  onChange,
}: {
  label: string;
  description: string;
  active: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onChange(!active)}
      role="switch"
      aria-checked={active}
      className="flex items-start justify-between gap-4 py-2.5 text-left transition-colors hover:bg-accent/30 -mx-2 px-2 rounded"
    >
      <div className="min-w-0 flex-1">
        <div className="text-[13.5px] font-medium text-foreground">{label}</div>
        <div className="mt-0.5 text-[12px] leading-[1.5] text-muted-foreground">
          {description}
        </div>
      </div>
      <span
        aria-hidden
        className={cn(
          "mt-0.5 flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors",
          active
            ? "border-primary/60 bg-primary/20"
            : "border-border bg-muted",
        )}
      >
        <span
          className={cn(
            "block size-3.5 rounded-full bg-background shadow-sm transition-transform",
            active ? "translate-x-4" : "translate-x-0.5",
            active ? "bg-primary" : "bg-muted-foreground/60",
          )}
        />
      </span>
    </button>
  );
}

// ---------------------------------------------------------------------------
// Per-source card
// ---------------------------------------------------------------------------

interface SourceCardProps {
  sourceType: string;
  label: string;
  knobs: SourceKnobs;
  trustDisabled: boolean;
  dirtyFromDefaults: boolean;
  onChange: (knobs: SourceKnobs) => void;
  onReset: () => void;
}

function SourceCard({
  sourceType,
  label,
  knobs,
  trustDisabled,
  dirtyFromDefaults,
  onChange,
  onReset,
}: SourceCardProps) {
  const presets = useMemo(() => PRESETS[sourceType] ?? {}, [sourceType]);
  const activePreset = useMemo(() => {
    for (const [name, p] of Object.entries(presets)) {
      if (sameKnobs(knobs, p)) return name;
    }
    return "Custom";
  }, [knobs, presets]);

  return (
    <article
      className="relative overflow-hidden rounded-lg border border-border bg-card"
      style={
        {
          "--chip-hue": `var(--source-${sourceType}, var(--source-default))`,
        } as React.CSSProperties
      }
    >
      {/* Tonal spine — full-height accent flush against the card edge so
          it reads as part of the card's tonal identity rather than a
          floating stripe. */}
      <span
        aria-hidden
        className="absolute inset-y-0 left-0 w-[3px]"
        style={{
          backgroundColor:
            "color-mix(in oklch, var(--chip-hue) 55%, transparent)",
        }}
      />

      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border/60 p-4">
        <div className="flex items-center gap-3">
          <ConnectorLogo type={sourceType} size="sm" />
          <div className="leading-tight">
            <div className="text-[15px] font-medium text-foreground">{label}</div>
            <SourceChip type={sourceType} />
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          {Object.keys(presets).map((name) => {
            const isActive = activePreset === name;
            return (
              <button
                key={name}
                type="button"
                onClick={() => onChange(presets[name])}
                className={cn(
                  "rounded-full px-2.5 py-1 text-[11px] font-medium transition-colors",
                  isActive
                    ? "bg-primary/15 text-[color:var(--primary)]"
                    : "bg-muted text-muted-foreground hover:bg-muted-foreground/15 hover:text-foreground",
                )}
              >
                {name}
              </button>
            );
          })}
          <span
            className={cn(
              "rounded-full px-2.5 py-1 text-[11px] font-medium",
              activePreset === "Custom"
                ? "bg-primary/10 text-[color:var(--primary)]"
                : "text-muted-foreground/60",
            )}
          >
            Custom
          </span>
        </div>
      </div>

      <div className="grid gap-6 p-4 md:grid-cols-[1fr_220px] md:gap-10">
        <div className="flex flex-col gap-5">
          <SliderRow
            label="Freshness half-life"
            suffix={halfLifeLabel(knobs.halfLife)}
            value={knobs.halfLife}
            min={1}
            max={365}
            step={1}
            onChange={(v) => onChange({ ...knobs, halfLife: v })}
          />
          <SliderRow
            label="Stale floor"
            suffix={`${Math.round(knobs.floor * 100)}%`}
            value={Math.round(knobs.floor * 100)}
            min={0}
            max={100}
            step={5}
            onChange={(v) => onChange({ ...knobs, floor: v / 100 })}
          />
          <SliderRow
            label="Trust weight"
            suffix={trustLabel(knobs.trust)}
            value={Math.round(knobs.trust * 100)}
            min={50}
            max={150}
            step={5}
            disabled={trustDisabled}
            hint={trustDisabled ? "Trust weights disabled in Signals above" : undefined}
            onChange={(v) => onChange({ ...knobs, trust: v / 100 })}
          />
          <p className="text-[12.5px] leading-[1.55] text-muted-foreground">
            {plainLanguage(knobs, trustDisabled)}
          </p>
        </div>

        <DecayCurve halfLife={knobs.halfLife} floor={knobs.floor} />
      </div>

      {dirtyFromDefaults && (
        <div className="flex justify-end border-t border-border/60 px-4 py-2">
          <button
            type="button"
            onClick={onReset}
            className="inline-flex items-center gap-1 text-[12px] text-muted-foreground transition-colors hover:text-foreground"
          >
            <RotateCcw className="size-3" aria-hidden />
            Reset this source
          </button>
        </div>
      )}
    </article>
  );
}

function SliderRow({
  label,
  suffix,
  value,
  min,
  max,
  step,
  disabled,
  hint,
  onChange,
}: {
  label: string;
  suffix: string;
  value: number;
  min: number;
  max: number;
  step: number;
  disabled?: boolean;
  hint?: string;
  onChange: (v: number) => void;
}) {
  return (
    <div className={cn("flex flex-col gap-1.5", disabled && "opacity-60")}>
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-[13px] font-medium text-foreground">{label}</span>
        <span className="font-mono text-[12.5px] tabular-nums text-muted-foreground">
          {suffix}
        </span>
      </div>
      <Slider
        value={value}
        onValueChange={onChange}
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        aria-label={label}
      />
      {hint && (
        <p className="text-[11.5px] text-muted-foreground/70">{hint}</p>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Decay curve visualization
// ---------------------------------------------------------------------------

function DecayCurve({ halfLife, floor }: { halfLife: number; floor: number }) {
  // Outer canvas is generous: left padding gives the Y-axis labels their
  // own gutter (otherwise "100" overlaps the curve at the top-left),
  // bottom padding gives the X-axis labels room below the axis line.
  const width = 220;
  const height = 110;
  const padding = { top: 14, right: 14, bottom: 22, left: 28 };
  const innerW = width - padding.left - padding.right;
  const innerH = height - padding.top - padding.bottom;

  const points = useMemo(() => {
    const samples = 80;
    const xs: string[] = [];
    for (let i = 0; i <= samples; i++) {
      const ageDays = (i / samples) * 365;
      const freshness = Math.pow(0.5, ageDays / halfLife);
      const factor = floor + (1 - floor) * freshness;
      const x = padding.left + (ageDays / 365) * innerW;
      const y = padding.top + (1 - factor) * innerH;
      xs.push(`${x.toFixed(1)},${y.toFixed(1)}`);
    }
    return xs.join(" ");
  }, [halfLife, floor, innerH, innerW, padding.left, padding.top]);

  const halfLifeX = padding.left + (Math.min(halfLife, 365) / 365) * innerW;
  const axisY = padding.top + innerH;
  const topY = padding.top;

  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
        Decay curve
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="h-[110px] w-full"
        aria-hidden
      >
        {/* Axis baseline (0%) */}
        <line
          x1={padding.left}
          y1={axisY}
          x2={padding.left + innerW}
          y2={axisY}
          stroke="var(--border)"
          strokeWidth={1}
        />
        {/* 100% top guide */}
        <line
          x1={padding.left}
          y1={topY}
          x2={padding.left + innerW}
          y2={topY}
          stroke="var(--border)"
          strokeDasharray="2 3"
          strokeWidth={1}
          opacity={0.5}
        />
        {/* Floor guide */}
        <line
          x1={padding.left}
          y1={padding.top + (1 - floor) * innerH}
          x2={padding.left + innerW}
          y2={padding.top + (1 - floor) * innerH}
          stroke="var(--primary)"
          strokeDasharray="2 3"
          strokeWidth={1}
          opacity={0.3}
        />
        {/* Half-life marker */}
        {halfLife <= 365 && (
          <line
            x1={halfLifeX}
            y1={topY}
            x2={halfLifeX}
            y2={axisY}
            stroke="var(--primary)"
            strokeDasharray="2 3"
            strokeWidth={1}
            opacity={0.5}
          />
        )}
        {/* The curve */}
        <polyline
          points={points}
          fill="none"
          stroke="var(--primary)"
          strokeWidth={2}
          style={{ transition: "all 120ms ease" }}
        />
        {/* Y-axis labels — sitting in the left gutter, right-anchored and
            vertically centered on the guide they describe. */}
        <text
          x={padding.left - 6}
          y={topY}
          fontSize="9"
          textAnchor="end"
          dominantBaseline="middle"
          fill="var(--muted-foreground)"
        >
          100
        </text>
        <text
          x={padding.left - 6}
          y={axisY}
          fontSize="9"
          textAnchor="end"
          dominantBaseline="middle"
          fill="var(--muted-foreground)"
        >
          0
        </text>
        {/* X-axis labels — sitting below the axis. */}
        <text
          x={padding.left}
          y={axisY + 12}
          fontSize="9"
          textAnchor="start"
          fill="var(--muted-foreground)"
        >
          0d
        </text>
        <text
          x={padding.left + innerW}
          y={axisY + 12}
          fontSize="9"
          textAnchor="end"
          fill="var(--muted-foreground)"
        >
          365d
        </text>
        {/* Half-life marker label — placed INSIDE the plot, just above the
            axis baseline and offset from the dashed line, so it's always
            visible regardless of where the marker sits. Flips to the left
            side of the line when the marker is near the right edge. */}
        {halfLife <= 365 && (() => {
          const nearRight = halfLifeX > padding.left + innerW - 36;
          return (
            <text
              x={nearRight ? halfLifeX - 4 : halfLifeX + 4}
              y={axisY - 4}
              fontSize="9"
              textAnchor={nearRight ? "end" : "start"}
              fill="var(--primary)"
            >
              {halfLife}d
            </text>
          );
        })()}
      </svg>
    </div>
  );
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

function sameKnobs(a: SourceKnobs, b: SourceKnobs): boolean {
  return (
    a.halfLife === b.halfLife &&
    Math.abs(a.floor - b.floor) < 1e-6 &&
    Math.abs(a.trust - b.trust) < 1e-6
  );
}

function sameRanking(a: RankingSettings, b: RankingSettings): boolean {
  if (a.metadata_bonus_enabled !== b.metadata_bonus_enabled) return false;
  if (a.source_trust_enabled !== b.source_trust_enabled) return false;
  for (const src of SOURCE_ORDER) {
    if ((a.source_half_life_days[src] ?? -1) !== (b.source_half_life_days[src] ?? -1)) return false;
    if (Math.abs((a.source_recency_floor[src] ?? -1) - (b.source_recency_floor[src] ?? -1)) > 1e-6) return false;
    if (Math.abs((a.source_trust_weight[src] ?? -1) - (b.source_trust_weight[src] ?? -1)) > 1e-6) return false;
  }
  return true;
}

function halfLifeLabel(days: number): string {
  if (days === 1) return "1 day";
  if (days < 30) return `${days} days`;
  if (days < 365) {
    const m = days / 30;
    if (m === 1) return "1 month";
    return m % 1 === 0 ? `${m} months` : `~${m.toFixed(1)} months`;
  }
  return days === 365 ? "1 year" : `~${(days / 365).toFixed(1)} years`;
}

function trustLabel(weight: number): string {
  if (Math.abs(weight - 1) < 1e-6) return "neutral";
  const pct = Math.round((weight - 1) * 100);
  return pct > 0 ? `+${pct}%` : `${pct}%`;
}

function plainLanguage(knobs: SourceKnobs, trustDisabled: boolean): string {
  const halfTxt = halfLifeLabel(knobs.halfLife);
  const floorPct = Math.round(knobs.floor * 100);
  const trustTxt = trustLabel(knobs.trust);
  const trustPhrase = trustDisabled
    ? "Trust disabled globally."
    : trustTxt === "neutral"
      ? "Neutral rerank weight."
      : trustTxt.startsWith("+")
        ? `Boosted ${trustTxt} at the rerank stage.`
        : `Penalized ${trustTxt.replace("-", "")} at the rerank stage.`;
  return `Half-relevance after ${halfTxt}, never dropping below ${floorPct}%. ${trustPhrase}`;
}
