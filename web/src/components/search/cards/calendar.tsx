import { CalendarDays, MapPin, Repeat, User, Users } from "lucide-react";
import type { CSSProperties } from "react";
import type { DocumentHit } from "@/lib/api-types";

// Calendar card content region. Composition intent: "an event torn off the
// wall calendar". The signature move is a mini date tile (month + day) tinted
// in the ical hue, sitting left of the event's particulars:
//   1. Date tile — month abbreviation over a large day number. Reads as a
//      tear-off calendar leaf; orients the whole card around *when*.
//   2. Particulars column — the time range (or "All day"), then the weekday
//      with an inline recurrence note ("Monday · Weekly").
//   3. Where / who rows — location and guest/organizer lines with glyphs.
//   4. Snippet — the matched highlight (when searching) or the event's own
//      description (when browsing), never the machine-formatted When/Where
//      footer the backend folds into `content` for retrieval.
//   5. Footer — the source calendar as a teal tag, plus a status badge for
//      tentative/cancelled events.
// The chassis above supplies the event title (SUMMARY) and the relative
// "in 2 days" timestamp; this body never repeats them.

interface Props {
  hit: DocumentHit;
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() ? v : undefined;
}

function strArr(v: unknown): string[] {
  return Array.isArray(v)
    ? v.filter((x): x is string => typeof x === "string")
    : [];
}

function parseDate(v: unknown): Date | null {
  if (typeof v !== "string" || !v) return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
}

const DAY_MS = 86_400_000;

function fmtMonthDay(d: Date): string {
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function fmtTime(d: Date): string {
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

// timeLine renders the clock-time range, or an all-day label that widens to a
// date span for multi-day events (DTEND is exclusive for all-day values, so we
// step back a day before showing the inclusive last date).
function timeLine(start: Date | null, end: Date | null, allDay: boolean): string | null {
  if (allDay) {
    if (start && end) {
      const days = Math.round((end.getTime() - start.getTime()) / DAY_MS);
      if (days > 1) {
        const last = new Date(end.getTime() - DAY_MS);
        return `All day · ${fmtMonthDay(start)} – ${fmtMonthDay(last)}`;
      }
    }
    return "All day";
  }
  if (!start) return null;
  if (!end) return fmtTime(start);
  if (start.toDateString() === end.toDateString()) {
    return `${fmtTime(start)} – ${fmtTime(end)}`;
  }
  return `${fmtMonthDay(start)} ${fmtTime(start)} – ${fmtMonthDay(end)} ${fmtTime(end)}`;
}

const FREQ_LABELS: Record<string, [string, string]> = {
  DAILY: ["Daily", "days"],
  WEEKLY: ["Weekly", "weeks"],
  MONTHLY: ["Monthly", "months"],
  YEARLY: ["Yearly", "years"],
  HOURLY: ["Hourly", "hours"],
};

// humanizeRRULE turns "FREQ=WEEKLY;INTERVAL=2" into "Every 2 weeks".
function humanizeRRULE(rrule?: string): string | null {
  if (!rrule) return null;
  let freq = "";
  let interval = 1;
  for (const part of rrule.split(";")) {
    const [k, v] = part.split("=");
    if (k?.toUpperCase() === "FREQ") freq = (v ?? "").toUpperCase();
    else if (k?.toUpperCase() === "INTERVAL")
      interval = Number.parseInt(v ?? "1", 10) || 1;
  }
  const label = FREQ_LABELS[freq];
  if (!label) return "Repeats";
  return interval === 1 ? label[0] : `Every ${interval} ${label[1]}`;
}

const TILE_BG = "color-mix(in oklch, var(--source-ical) 10%, transparent)";
const TILE_BORDER = "color-mix(in oklch, var(--source-ical) 26%, var(--border))";
const TILE_MONTH = "color-mix(in oklch, var(--source-ical) 62%, var(--foreground))";
const TAG_BG = "color-mix(in oklch, var(--source-ical) 12%, transparent)";
const TAG_BORDER = "color-mix(in oklch, var(--source-ical) 30%, transparent)";
const TAG_FG = "color-mix(in oklch, var(--source-ical) 68%, var(--foreground))";

export function CalendarCardBody({ hit }: Readonly<Props>) {
  const m = hit.metadata ?? {};
  const recurring = m.recurring === true;
  const allDay = m.all_day === true;

  const start = parseDate(m.dtstart);
  const end = parseDate(m.dtend);
  // The tile shows the occurrence that matters: the next (or most recent) for a
  // series, the start for a one-off. created_at is the backend's recency anchor
  // and a safe last resort.
  const tileDate =
    (recurring
      ? parseDate(m.next_occurrence) ?? parseDate(m.recent_occurrence)
      : null) ??
    start ??
    parseDate(hit.created_at);

  const when = timeLine(start, end, allDay);
  const weekday = tileDate?.toLocaleDateString(undefined, { weekday: "long" });
  const yearSuffix =
    tileDate && tileDate.getFullYear() !== new Date().getFullYear()
      ? ` · ${tileDate.getFullYear()}`
      : "";
  const recurrence = recurring ? humanizeRRULE(str(m.rrule)) : null;

  // Collapse line breaks (plus any surrounding spaces/tabs) into ", ".
  // The classes around the line break exclude \r\n so the quantifiers can't
  // overlap the separator — keeps the match linear (no ReDoS backtracking).
  const location = str(m.location)?.replace(/[^\S\r\n]*[\r\n]+[^\S\r\n]*/g, ", ");
  const attendees = strArr(m.attendees);
  const organizer = str(m.organizer);

  const calendar = str(m.calendar);
  const status = str(m.status)?.toUpperCase();
  const showStatus = status === "CANCELLED" || status === "TENTATIVE";

  // Only the event's own description — never the chassis headline, which would
  // highlight the machine-formatted When/Where/raw-timestamp footer the backend
  // folds into `content` for retrieval. The structured rows above already carry
  // the searchable particulars (time, place, people), so the highlight adds
  // nothing but noise here.
  const description = str(m.description);

  return (
    <div className="mt-2.5 flex flex-col gap-3">
      <div className="flex items-start gap-3">
        {tileDate && (
          <div
            style={
              { backgroundColor: TILE_BG, borderColor: TILE_BORDER } as CSSProperties
            }
            className="flex h-[54px] w-[50px] shrink-0 flex-col items-center justify-center rounded-lg border"
          >
            <span
              style={{ color: TILE_MONTH } as CSSProperties}
              className="text-[10px] font-bold uppercase leading-none tracking-[0.12em]"
            >
              {tileDate.toLocaleDateString(undefined, { month: "short" })}
            </span>
            <span className="mt-1 text-[21px] font-semibold leading-none tabular-nums text-foreground">
              {tileDate.toLocaleDateString(undefined, { day: "numeric" })}
            </span>
          </div>
        )}

        <div className="min-w-0 flex-1">
          {when && (
            <div className="text-[13.5px] font-medium leading-tight text-foreground">
              {when}
            </div>
          )}
          {(weekday || recurrence) && (
            <div className="mt-0.5 flex flex-wrap items-center gap-x-1.5 text-[12.5px] text-muted-foreground">
              {weekday && (
                <span>
                  {weekday}
                  {yearSuffix}
                </span>
              )}
              {recurrence && (
                <span className="inline-flex items-center gap-1">
                  <span aria-hidden className="text-muted-foreground/40">
                    ·
                  </span>
                  <Repeat className="size-3 text-muted-foreground/70" aria-hidden />
                  {recurrence}
                </span>
              )}
            </div>
          )}
        </div>
      </div>

      {(location || attendees.length > 0 || organizer) && (
        <div className="flex flex-col gap-1.5">
          {location && (
            <div className="flex min-w-0 items-center gap-1.5 text-[12.5px] text-muted-foreground">
              <MapPin
                className="size-3.5 shrink-0 text-muted-foreground/70"
                aria-hidden
              />
              <span className="truncate" title={location}>
                {location}
              </span>
            </div>
          )}
          {attendees.length > 0 ? (
            <div className="flex min-w-0 items-center gap-1.5 text-[12.5px] text-muted-foreground">
              <Users
                className="size-3.5 shrink-0 text-muted-foreground/70"
                aria-hidden
              />
              <span className="truncate" title={attendees.join(", ")}>
                {attendees.length} {attendees.length === 1 ? "guest" : "guests"}
              </span>
            </div>
          ) : (
            organizer && (
              <div className="flex min-w-0 items-center gap-1.5 text-[12.5px] text-muted-foreground">
                <User
                  className="size-3.5 shrink-0 text-muted-foreground/70"
                  aria-hidden
                />
                <span className="truncate" title={organizer}>
                  {organizer}
                </span>
              </div>
            )
          )}
        </div>
      )}

      {description && (
        <p className="line-clamp-2 text-[13.5px] leading-[1.55] text-muted-foreground">
          {description}
        </p>
      )}

      {(calendar || showStatus) && (
        <div className="flex flex-wrap items-center gap-1.5">
          {calendar && (
            <span
              style={
                {
                  backgroundColor: TAG_BG,
                  borderColor: TAG_BORDER,
                  color: TAG_FG,
                } as CSSProperties
              }
              className="inline-flex h-5 items-center gap-1 rounded-full border px-2 text-[11.5px] font-medium leading-none"
              title={`Calendar: ${calendar}`}
            >
              <CalendarDays className="size-3" aria-hidden />
              {calendar}
            </span>
          )}
          {showStatus && (
            <span
              className={
                status === "CANCELLED"
                  ? "inline-flex h-5 items-center rounded-full border border-destructive/35 bg-destructive/10 px-2 text-[11px] font-semibold uppercase tracking-wide leading-none text-destructive"
                  : "inline-flex h-5 items-center rounded-full border border-amber-500/35 bg-amber-500/10 px-2 text-[11px] font-semibold uppercase tracking-wide leading-none text-amber-600 dark:text-amber-400"
              }
            >
              {status === "CANCELLED" ? "Cancelled" : "Tentative"}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
