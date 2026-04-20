import { isSameDay } from "date-fns";
import type { MessageRowModel, GroupPosition } from "./message-row";

export type TimelineItem =
  | { kind: "day"; id: string; date: Date }
  | { kind: "message"; model: MessageRowModel };

interface BuildOptions {
  gapMinutes?: number;
}

function isSameBurst(
  a: MessageRowModel,
  b: MessageRowModel,
  da: Date,
  db: Date,
  gapMs: number,
): boolean {
  return (
    a.senderId !== null &&
    a.senderId === b.senderId &&
    isSameDay(da, db) &&
    Math.abs(db.getTime() - da.getTime()) < gapMs
  );
}

function dayMarker(d: Date): TimelineItem {
  return {
    kind: "day",
    id: `day-${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`,
    date: d,
  };
}

// 2-bit lookup from (samePrev, sameNext) to burst position.
const POSITION: Record<string, GroupPosition> = {
  "true|true": "mid",
  "true|false": "last",
  "false|true": "first",
  "false|false": "solo",
};

function burstPosition(samePrev: boolean, sameNext: boolean): GroupPosition {
  return POSITION[`${samePrev}|${sameNext}`];
}

export function buildTimeline(
  rows: MessageRowModel[],
  { gapMinutes = 5 }: BuildOptions = {},
): TimelineItem[] {
  const items: TimelineItem[] = [];
  const gapMs = gapMinutes * 60_000;
  let lastDate: Date | null = null;

  for (let i = 0; i < rows.length; i++) {
    const r = rows[i];
    const d = new Date(r.createdAt);

    if (!lastDate || !isSameDay(d, lastDate)) items.push(dayMarker(d));
    lastDate = d;

    const prev = rows[i - 1];
    const next = rows[i + 1];
    const samePrev =
      !!prev && isSameBurst(prev, r, new Date(prev.createdAt), d, gapMs);
    const sameNext =
      !!next && isSameBurst(r, next, d, new Date(next.createdAt), gapMs);

    items.push({
      kind: "message",
      model: { ...r, position: burstPosition(samePrev, sameNext) },
    });
  }

  return items;
}
