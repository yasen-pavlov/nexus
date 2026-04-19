import { memo, useMemo } from "react";
import { DayDivider } from "./day-divider";
import { MessageRow, type MessageRowModel } from "./message-row";
import { buildTimeline } from "./timeline";

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
