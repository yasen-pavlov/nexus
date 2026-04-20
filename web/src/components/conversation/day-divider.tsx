import { format } from "date-fns";

interface Props {
  date: Date;
}

export function DayDivider({ date }: Readonly<Props>) {
  return (
    <div
      role="separator"
      aria-label={format(date, "EEEE, MMMM d, yyyy")}
      className="sticky top-0 z-10 flex items-center gap-3 py-2"
    >
      <div className="h-px flex-1 bg-border/60" aria-hidden />
      <span className="rounded-full border border-border/60 bg-background px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/90">
        {format(date, "EEEE · MMM d, yyyy")}
      </span>
      <div className="h-px flex-1 bg-border/60" aria-hidden />
    </div>
  );
}
