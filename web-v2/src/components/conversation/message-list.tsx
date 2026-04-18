import { memo, useMemo } from "react";
import { isSameDay } from "date-fns";
import { DayDivider } from "./day-divider";
import { MessageRow, type MessageRowModel, type GroupPosition } from "./message-row";

export type TimelineItem =
  | { kind: "day"; id: string; date: Date }
  | { kind: "message"; model: MessageRowModel };

interface BuildOptions {
  gapMinutes?: number;
}

export function buildTimeline(
  rows: MessageRowModel[],
  { gapMinutes = 5 }: BuildOptions = {},
): TimelineItem[] {
  const items: TimelineItem[] = [];
  const gapMs = gapMinutes * 60_000;
  let lastDate: Date | null = null;

  const sameBurst = (
    a: MessageRowModel,
    b: MessageRowModel,
    da: Date,
    db: Date,
  ) =>
    a.senderId !== null &&
    a.senderId === b.senderId &&
    isSameDay(da, db) &&
    Math.abs(db.getTime() - da.getTime()) < gapMs;

  for (let i = 0; i < rows.length; i++) {
    const r = rows[i]!;
    const d = new Date(r.createdAt);

    if (!lastDate || !isSameDay(d, lastDate)) {
      items.push({
        kind: "day",
        id: `day-${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`,
        date: d,
      });
    }
    lastDate = d;

    const prev = rows[i - 1];
    const next = rows[i + 1];
    const prevDate = prev ? new Date(prev.createdAt) : null;
    const nextDate = next ? new Date(next.createdAt) : null;

    const samePrev = !!prev && !!prevDate && sameBurst(prev, r, prevDate, d);
    const sameNext = !!next && !!nextDate && sameBurst(r, next, d, nextDate);

    const position: GroupPosition =
      samePrev && sameNext
        ? "mid"
        : samePrev
          ? "last"
          : sameNext
            ? "first"
            : "solo";

    items.push({ kind: "message", model: { ...r, position } });
  }

  return items;
}

interface Props {
  rows: MessageRowModel[];
}

export const MessageList = memo(function MessageList({ rows }: Props) {
  const items = useMemo(() => buildTimeline(rows), [rows]);

  return (
    <div className="flex flex-col">
      {items.map((it) =>
        it.kind === "day" ? (
          <DayDivider key={it.id} date={it.date} />
        ) : (
          <MessageRow key={it.model.sourceId} model={it.model} />
        ),
      )}
    </div>
  );
});
