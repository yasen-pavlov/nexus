import { useFormContext } from "react-hook-form";
import { format, subDays, subMonths } from "date-fns";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

/**
 * Sync-window control. Writes `config.sync_since` as a YYYY-MM-DD string
 * (or empty = "sync all history"). Shared across connector types that
 * support time-bounded sync — the backend's `ComputeSyncSince` reads the
 * same key regardless of connector.
 *
 * Two mutually-exclusive modes:
 *   - "all"   → config.sync_since = ""  (no cutoff)
 *   - "since" → config.sync_since = "YYYY-MM-DD"
 *
 * Preset buttons below the date input fill the field with common
 * relative windows (7 / 30 / 90 days / 1 year). The raw input is still
 * editable so users can pick any date they want.
 */
export function SyncWindowField() {
  const { watch, setValue } = useFormContext();
  const value = (watch("config.sync_since") as string | undefined) ?? "";
  const mode: "all" | "since" = value ? "since" : "all";

  const set = (next: string) => {
    setValue("config.sync_since", next, { shouldDirty: true });
  };

  const preset = (builder: (today: Date) => Date) => {
    const d = builder(new Date());
    set(format(d, "yyyy-MM-dd"));
  };

  return (
    <div className="space-y-3">
      <div role="tablist" className="flex gap-1 rounded-full border border-border bg-muted/30 p-1">
        <button
          type="button"
          role="tab"
          aria-selected={mode === "all"}
          onClick={() => set("")}
          className={cn(
            "flex-1 rounded-full px-3 py-1.5 text-[12.5px] font-medium transition-colors",
            mode === "all"
              ? "bg-foreground text-background"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          All history
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={mode === "since"}
          onClick={() => {
            if (!value) preset((d) => subDays(d, 30));
          }}
          className={cn(
            "flex-1 rounded-full px-3 py-1.5 text-[12.5px] font-medium transition-colors",
            mode === "since"
              ? "bg-foreground text-background"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          Since date
        </button>
      </div>

      {mode === "since" && (
        <div className="space-y-3 rounded-lg border border-border bg-card p-4">
          <div className="space-y-1.5">
            <label
              htmlFor="sync-since"
              className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80"
            >
              Sync from
            </label>
            <Input
              id="sync-since"
              type="date"
              value={value}
              max={format(new Date(), "yyyy-MM-dd")}
              onChange={(e) => set(e.target.value)}
              className="font-mono text-[13px]"
            />
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <PresetButton onClick={() => preset((d) => subDays(d, 7))}>7 days</PresetButton>
            <PresetButton onClick={() => preset((d) => subDays(d, 30))}>30 days</PresetButton>
            <PresetButton onClick={() => preset((d) => subDays(d, 90))}>90 days</PresetButton>
            <PresetButton onClick={() => preset((d) => subMonths(d, 6))}>6 months</PresetButton>
            <PresetButton onClick={() => preset((d) => subMonths(d, 12))}>1 year</PresetButton>
          </div>
          <p className="text-[12px] text-muted-foreground">
            Documents older than this date won&apos;t be indexed on the next sync. You can always
            widen the window later; the connector&apos;s cursor will pick up any newly-in-range
            items.
          </p>
        </div>
      )}

      {mode === "all" && (
        <div className="rounded-lg border border-dashed border-border bg-card/60 p-4 text-[12.5px] text-muted-foreground">
          No cutoff — the connector indexes everything it can see. For large mailboxes or
          chat histories, consider a date window to speed up the first sync.
        </div>
      )}
    </div>
  );
}

function PresetButton({ children, onClick }: { children: React.ReactNode; onClick: () => void }) {
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="h-7 rounded-full px-3 text-[12px]"
      onClick={onClick}
    >
      Last {children}
    </Button>
  );
}
