import { useState } from "react";
import { PlayCircle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";

import {
  useRetentionSettings,
  type UseRetentionSettings,
} from "@/hooks/use-retention";
import type { RetentionSettings } from "@/lib/api-types";

export function RetentionForm() {
  const ctx = useRetentionSettings();

  if (ctx.isPending || !ctx.data) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-9 w-full max-w-xl" />
      </div>
    );
  }

  // Remount whenever the server-side settings object changes so the inner
  // form's useState seeds fresh from the new data without needing
  // useEffect-driven sync (which would trip the React Compiler rules
  // about setState inside useEffect + ref reads during render).
  return <RetentionFormInner key={fingerprint(ctx.data)} ctx={ctx} />;
}

function fingerprint(s: RetentionSettings): string {
  return `${s.retention_days}|${s.retention_per_connector}|${s.sweep_interval_minutes}`;
}

function RetentionFormInner({ ctx }: Readonly<{ ctx: UseRetentionSettings }>) {
  const { data, update, runSweep } = ctx;
  const saved = data!;
  const min = saved.min_sweep_interval_minutes ?? 5;

  const [form, setForm] = useState({
    retention_days: saved.retention_days,
    retention_per_connector: saved.retention_per_connector,
    sweep_interval_minutes: saved.sweep_interval_minutes,
  });

  const dirty =
    form.retention_days !== saved.retention_days ||
    form.retention_per_connector !== saved.retention_per_connector ||
    form.sweep_interval_minutes !== saved.sweep_interval_minutes;

  const invalidDays = form.retention_days < 0;
  const invalidPerConn = form.retention_per_connector < 0;
  const invalidSweep = form.sweep_interval_minutes < min;
  const invalid = invalidDays || invalidPerConn || invalidSweep;

  const revert = () => {
    setForm({
      retention_days: saved.retention_days,
      retention_per_connector: saved.retention_per_connector,
      sweep_interval_minutes: saved.sweep_interval_minutes,
    });
  };

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        if (invalid) return;
        update.mutate(form);
      }}
    >
      <div className="grid max-w-xl gap-5 sm:grid-cols-2">
        <NumberField
          label="Retention (days)"
          hint="0 disables the age-based cutoff."
          value={form.retention_days}
          min={0}
          invalid={invalidDays}
          onChange={(v) => setForm((f) => ({ ...f, retention_days: v }))}
        />
        <NumberField
          label="Per-connector cap"
          hint="0 disables the per-connector cap."
          value={form.retention_per_connector}
          min={0}
          invalid={invalidPerConn}
          onChange={(v) =>
            setForm((f) => ({ ...f, retention_per_connector: v }))
          }
        />
        <NumberField
          label="Sweep interval (min)"
          hint={`Minimum ${min} minutes — the sweeper refuses to tick faster.`}
          value={form.sweep_interval_minutes}
          min={min}
          invalid={invalidSweep}
          onChange={(v) =>
            setForm((f) => ({ ...f, sweep_interval_minutes: v }))
          }
        />
      </div>

      <div className="flex max-w-xl items-start gap-2.5 rounded-md border border-border/70 bg-background/40 p-3 text-[13px]">
        <PlayCircle
          className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/80"
          aria-hidden
        />
        <div className="flex-1 leading-[1.55]">
          <div className="font-medium text-foreground">Run cleanup now</div>
          <div className="text-muted-foreground">
            Apply the currently-persisted retention rules once, without
            waiting for the next tick. Safe any time.
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => runSweep.mutate()}
          disabled={runSweep.isPending}
        >
          {runSweep.isPending ? "Running…" : "Run now"}
        </Button>
      </div>

      {dirty && (
        <div className="sticky bottom-0 -mx-6 -mb-6 mt-4 flex items-center justify-between gap-3 border-t border-border/70 bg-card/95 px-6 py-3 backdrop-blur">
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
            <Button
              type="submit"
              size="sm"
              disabled={update.isPending || invalid}
            >
              {update.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        </div>
      )}
    </form>
  );
}

function NumberField({
  label,
  hint,
  value,
  min,
  invalid,
  onChange,
}: Readonly<{
  label: string;
  hint?: string;
  value: number;
  min: number;
  invalid?: boolean;
  onChange: (v: number) => void;
}>) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label className="text-[13px] font-medium">{label}</Label>
      <Input
        type="number"
        inputMode="numeric"
        min={min}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className={
          invalid
            ? "h-10 border-destructive/60 font-mono text-[13px]"
            : "h-10 font-mono text-[13px]"
        }
      />
      {hint && (
        <p className="text-[12px] leading-[1.5] text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  );
}
