import { CronExpressionParser } from "cron-parser";
import { useState } from "react";
import { toast } from "sonner";

import { ScheduleField } from "@/components/connectors/schedule-field";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";

interface ScheduleTabProps {
  schedule: string;
  canManage: boolean;
  onSave: (next: string) => Promise<unknown>;
  onClearCursor: () => void;
}

function isValidSchedule(expr: string): boolean {
  if (expr.trim() === "") return true; // empty = manual trigger only
  try {
    CronExpressionParser.parse(expr);
    return true;
  } catch {
    return false;
  }
}

/**
 * Schedule editor tab. Edits a LOCAL draft and commits it with an explicit
 * "Save schedule" button — not a PUT on every ScheduleField change, which
 * previously fired one request per keystroke in the Custom cron input,
 * persisting incomplete-but-valid intermediates and 400-ing the rest silently.
 * Validates client-side and surfaces the save result as a toast.
 */
export function ScheduleTab({
  schedule,
  canManage,
  onSave,
  onClearCursor,
}: Readonly<ScheduleTabProps>) {
  const [draft, setDraft] = useState(schedule);
  const [baseline, setBaseline] = useState(schedule);
  const [saving, setSaving] = useState(false);

  // Re-baseline the draft when a successful save (+ refetch) or any external
  // change updates the schedule prop. Done during render (React's recommended
  // alternative to a setState-in-effect) so there's no extra render pass.
  if (schedule !== baseline) {
    setBaseline(schedule);
    setDraft(schedule);
  }

  const dirty = draft !== schedule;
  const valid = isValidSchedule(draft);

  const handleSave = async () => {
    setSaving(true);
    try {
      await onSave(draft);
      toast.success("Schedule updated.");
    } catch {
      toast.error("Couldn't update schedule.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4 rounded-xl border border-border bg-card p-6">
      <ScheduleField value={draft} onChange={setDraft} />
      <div className="flex justify-end">
        <Button
          size="sm"
          disabled={!canManage || !dirty || !valid || saving}
          onClick={() => void handleSave()}
        >
          Save schedule
        </Button>
      </div>
      <Separator />
      <div className="flex items-center justify-between text-[12.5px] text-muted-foreground">
        <span>Force a full re-sync if indexed data looks stale.</span>
        <Button variant="ghost" size="sm" disabled={!canManage} onClick={onClearCursor}>
          Clear cursor
        </Button>
      </div>
    </div>
  );
}
