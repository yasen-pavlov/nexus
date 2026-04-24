import { Fragment, type ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

export interface MetaItem {
  /** Unique key — e.g. "date", "size". */
  key: string;
  icon?: LucideIcon;
  label: ReactNode;
  /** Tooltip text. */
  title?: string;
  /** Render value in tabular-nums (dates, sizes, counts). */
  numeric?: boolean;
}

interface Props {
  // Accept all the common falsy values so callers can write
  // `conditionalString && { ... }` without TS rejecting the `""` branch.
  items: ReadonlyArray<MetaItem | false | null | undefined | "" | 0>;
  className?: string;
  /** Separator between items — defaults to the middot glyph. */
  separator?: ReactNode;
}

// MetaRow renders a muted, inline row of small metadata chunks with a
// middot separator. Callers pass a sparse array and we skip the falsy
// entries so guard logic stays where the data lives.
export function MetaRow({
  items,
  className,
  separator = "·",
}: Readonly<Props>) {
  const visible = items.filter((i): i is MetaItem => !!i);
  if (visible.length === 0) return null;

  return (
    <div
      className={cn(
        "flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[12px] text-muted-foreground",
        className,
      )}
    >
      {visible.map((item, index) => {
        const Icon = item.icon;
        return (
          <Fragment key={item.key}>
            {index > 0 && (
              <span
                aria-hidden
                className="shrink-0 text-muted-foreground/40"
              >
                {separator}
              </span>
            )}
            <span
              title={item.title}
              className={cn(
                "inline-flex items-center gap-1 leading-none",
                item.numeric && "tabular-nums",
              )}
            >
              {Icon && (
                <Icon
                  className="size-3 shrink-0 text-muted-foreground/70"
                  aria-hidden
                />
              )}
              {item.label}
            </span>
          </Fragment>
        );
      })}
    </div>
  );
}
