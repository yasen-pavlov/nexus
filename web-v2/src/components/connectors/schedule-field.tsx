import { useMemo, useState } from "react";
import { addMinutes, format } from "date-fns";
import cronstrue from "cronstrue";
import { CronExpressionParser } from "cron-parser";

import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/input";

type Preset = "off" | "hourly" | "daily" | "weekly" | "custom";

const PRESET_ORDER: Preset[] = ["off", "hourly", "daily", "weekly", "custom"];
const PRESET_LABEL: Record<Preset, string> = {
  off: "Off",
  hourly: "Hourly",
  daily: "Daily",
  weekly: "Weekly",
  custom: "Custom",
};

// Mon..Sun for display — cron dow uses 0..6 with 0 = Sunday, so we keep a
// parallel array to translate between visual order and the stored value.
const DAY_DISPLAY = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const DAY_CRON = [1, 2, 3, 4, 5, 6, 0];

// Preset detection from an existing cron string. Narrow enough regexes
// that ambiguous cases (e.g. "0 9 * * 1" could be weekly-on-mondays) route
// to the most specific preset that matches.
function detectPreset(expr: string): Preset {
  const trimmed = expr.trim();
  if (!trimmed) return "off";
  if (/^0 \* \* \* \*$/.test(trimmed)) return "hourly";
  if (/^\d+ \d+ \* \* \*$/.test(trimmed)) return "daily";
  if (/^\d+ \d+ \* \* [0-6,]+$/.test(trimmed)) return "weekly";
  return "custom";
}

function parseDaily(expr: string): { hour: number; minute: number } {
  const m = expr.match(/^(\d+) (\d+) \* \* \*$/);
  return m ? { hour: Number(m[2]), minute: Number(m[1]) } : { hour: 9, minute: 0 };
}
function parseWeekly(expr: string): { hour: number; minute: number; days: number[] } {
  const m = expr.match(/^(\d+) (\d+) \* \* ([0-6,]+)$/);
  if (!m) return { hour: 9, minute: 0, days: [1] };
  return {
    hour: Number(m[2]),
    minute: Number(m[1]),
    days: m[3].split(",").map(Number),
  };
}

export interface ScheduleFieldProps {
  value: string;
  onChange: (next: string) => void;
  className?: string;
}

/**
 * Preset-first schedule builder. The segmented control picks a cadence
 * shape (Off / Hourly / Daily / Weekly / Custom) and expands the matching
 * body below: an hour ruler for Daily, a day strip + hour ruler for
 * Weekly, a raw cron input for Custom. A status plaque at the bottom
 * translates the stored expression to English via cronstrue and shows
 * the next fire time via cron-parser.
 */
export function ScheduleField({ value, onChange, className }: ScheduleFieldProps) {
  const preset = detectPreset(value);
  const [customDraft, setCustomDraft] = useState(value);

  const setHourly = () => onChange("0 * * * *");
  const setDaily = (hour = 9, minute = 0) => onChange(`${minute} ${hour} * * *`);
  const setWeekly = (days: number[], hour = 9, minute = 0) => {
    const dow = [...days].sort((a, b) => a - b).join(",") || "1";
    onChange(`${minute} ${hour} * * ${dow}`);
  };
  const setCustom = (expr: string) => {
    setCustomDraft(expr);
    onChange(expr);
  };

  const daily = parseDaily(value);
  const weekly = parseWeekly(value);

  const description = useMemo(() => {
    if (!value.trim()) return "Manual trigger only";
    try {
      return cronstrue.toString(value, { use24HourTimeFormat: true });
    } catch {
      return "Invalid cron expression";
    }
  }, [value]);

  const nextRun = useMemo<Date | null>(() => {
    if (!value.trim()) return null;
    try {
      return CronExpressionParser.parse(value).next().toDate();
    } catch {
      return null;
    }
  }, [value]);

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <div
        role="tablist"
        className="flex gap-1 rounded-full border border-border bg-muted/30 p-1"
      >
        {PRESET_ORDER.map((p) => {
          const active = preset === p;
          return (
            <button
              key={p}
              role="tab"
              aria-selected={active}
              type="button"
              onClick={() => {
                if (p === "off") onChange("");
                else if (p === "hourly") setHourly();
                else if (p === "daily") setDaily(daily.hour, daily.minute);
                else if (p === "weekly") setWeekly(weekly.days, weekly.hour, weekly.minute);
                else if (p === "custom") setCustom(customDraft || "0 */4 * * *");
              }}
              className={cn(
                "flex-1 rounded-full px-3 py-1.5 text-[12.5px] font-medium transition-colors",
                active
                  ? "bg-foreground text-background"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {PRESET_LABEL[p]}
            </button>
          );
        })}
      </div>

      <div
        className={cn(
          "rounded-lg border border-border bg-card p-4 transition-all duration-200",
          preset === "off" && "opacity-70",
        )}
      >
        {preset === "off" && (
          <p className="text-[13px] text-muted-foreground">
            Nexus will not run this connector automatically. You can still trigger syncs from the
            connector page.
          </p>
        )}

        {preset === "hourly" && (
          <p className="text-[13px] text-muted-foreground">
            Runs at the top of every hour. Good for mailboxes and chat sources where freshness
            matters.
          </p>
        )}

        {preset === "daily" && (
          <HourRuler hour={daily.hour} onChange={(h) => setDaily(h, daily.minute)} />
        )}

        {preset === "weekly" && (
          <div className="space-y-4">
            <DayStrip
              selected={new Set(weekly.days)}
              onChange={(next) => setWeekly(Array.from(next), weekly.hour, weekly.minute)}
            />
            <HourRuler
              hour={weekly.hour}
              onChange={(h) => setWeekly(weekly.days, h, weekly.minute)}
            />
          </div>
        )}

        {preset === "custom" && (
          <div className="space-y-2">
            <label className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
              Cron expression · minute hour dom month dow
            </label>
            <Input
              value={customDraft}
              onChange={(e) => setCustom(e.target.value)}
              placeholder="0 */4 * * *"
              spellCheck={false}
              className="font-mono text-[13px]"
            />
          </div>
        )}
      </div>

      <div className="flex items-center justify-between gap-3 px-1 text-[12px]">
        <div className="truncate text-muted-foreground">
          <span className="mr-1 text-foreground/80">⏱</span> {description}
        </div>
        {nextRun && (
          <div className="tabular-nums text-muted-foreground/80">
            Next:{" "}
            <span className="text-foreground/80">{format(nextRun, "MMM d · HH:mm")}</span>
            {" "}
            <span className="text-muted-foreground">
              ({format(addMinutes(nextRun, 0), "zzz")})
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

function HourRuler({
  hour,
  onChange,
}: {
  hour: number;
  onChange: (hour: number) => void;
}) {
  return (
    <div>
      <div className="mb-2 flex items-baseline justify-between">
        <label className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
          Run at
        </label>
        <span className="text-[13px] tabular-nums text-foreground">
          {String(hour).padStart(2, "0")}:00
        </span>
      </div>
      <div>
        <div className="flex h-10 items-end gap-[2px]">
          {Array.from({ length: 24 }).map((_, h) => {
            const active = h === hour;
            const isMajor = h % 6 === 0;
            return (
              <button
                key={h}
                type="button"
                onClick={() => onChange(h)}
                aria-label={`${String(h).padStart(2, "0")}:00`}
                className={cn(
                  "flex-1 rounded-sm transition-colors",
                  active ? "bg-primary" : "bg-border/80 hover:bg-foreground/40",
                )}
                style={{ height: active ? 32 : isMajor ? 18 : 10 }}
              />
            );
          })}
        </div>
        <div className="mt-1 flex justify-between text-[10px] tabular-nums text-muted-foreground/70">
          <span>00</span>
          <span>06</span>
          <span>12</span>
          <span>18</span>
          <span>23</span>
        </div>
      </div>
    </div>
  );
}

function DayStrip({
  selected,
  onChange,
}: {
  selected: Set<number>;
  onChange: (next: Set<number>) => void;
}) {
  const toggle = (d: number) => {
    const next = new Set(selected);
    if (next.has(d)) next.delete(d);
    else next.add(d);
    if (next.size === 0) return; // require at least one day
    onChange(next);
  };
  return (
    <div>
      <label className="mb-2 block text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
        Days of week
      </label>
      <div className="flex gap-1.5">
        {DAY_DISPLAY.map((label, idx) => {
          const cronDay = DAY_CRON[idx];
          const active = selected.has(cronDay);
          return (
            <button
              key={label}
              type="button"
              onClick={() => toggle(cronDay)}
              aria-pressed={active}
              className={cn(
                "h-9 flex-1 rounded-md border text-[12px] font-medium transition-colors",
                active
                  ? "border-primary bg-primary/15 text-foreground"
                  : "border-border bg-card text-muted-foreground hover:bg-card-hover hover:text-foreground",
              )}
            >
              {label}
            </button>
          );
        })}
      </div>
    </div>
  );
}
