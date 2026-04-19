import { Cable, FileText, Folder, Inbox, MessageCircle } from "lucide-react";
import type { ComponentType, SVGProps } from "react";
import { cn } from "@/lib/utils";

type IconComponent = ComponentType<SVGProps<SVGSVGElement>>;

interface SourceMeta {
  label: string;
  icon: IconComponent;
  /** CSS var name that holds the tonal hue for this source. */
  colorVar: string;
}

const SOURCE_META: Record<string, SourceMeta> = {
  imap: { label: "Email", icon: Inbox, colorVar: "--source-imap" },
  telegram: {
    label: "Telegram",
    icon: MessageCircle,
    colorVar: "--source-telegram",
  },
  paperless: {
    label: "Paperless",
    icon: FileText,
    colorVar: "--source-paperless",
  },
  filesystem: {
    label: "Files",
    icon: Folder,
    colorVar: "--source-filesystem",
  },
};

const FALLBACK: SourceMeta = {
  label: "Source",
  icon: Cable,
  colorVar: "--source-default",
};

export function sourceMetaFor(type: string): SourceMeta {
  return SOURCE_META[type] ?? { ...FALLBACK, label: type || FALLBACK.label };
}

type Variant = "default" | "compact" | "pill";

interface Props {
  /** source_type from the DocumentHit (e.g. "imap"). */
  type: string;
  /** Override the auto-derived label. */
  label?: string;
  /** Optional trailing count (honored in pill variant). */
  count?: number;
  /** default: icon + label on cards. compact: icon only. pill: rounded with count. */
  variant?: Variant;
  /** Active/selected — inverts the fill. */
  active?: boolean;
  className?: string;
}

/**
 * A single source identity chip. One visual language across result cards,
 * filters, connectors, future RAG citations. Color comes from a CSS var
 * so light/dark theming is automatic.
 */
export function SourceChip({
  type,
  label,
  count,
  variant = "default",
  active,
  className,
}: Props) {
  const meta = sourceMetaFor(type);
  const Icon = meta.icon;
  const displayLabel = label ?? meta.label;

  const style = {
    "--chip-hue": `var(${meta.colorVar})`,
  } as React.CSSProperties;

  const base =
    "inline-flex shrink-0 items-center gap-1.5 leading-none select-none transition-colors";

  if (variant === "compact") {
    return (
      <span
        style={style}
        aria-label={displayLabel}
        title={displayLabel}
        className={cn(
          base,
          "size-5 justify-center rounded-md",
          "text-[color:var(--chip-hue)]",
          active
            ? "bg-[color-mix(in_oklch,var(--chip-hue)_18%,transparent)]"
            : "bg-[color-mix(in_oklch,var(--chip-hue)_10%,transparent)]",
          className,
        )}
      >
        <Icon className="size-3" aria-hidden />
      </span>
    );
  }

  if (variant === "pill") {
    return (
      <span
        style={style}
        className={cn(
          base,
          "h-6 rounded-full px-2 text-xs font-medium",
          active
            ? "bg-[color-mix(in_oklch,var(--chip-hue)_85%,transparent)] text-white"
            : "bg-[color-mix(in_oklch,var(--chip-hue)_12%,transparent)] text-[color:var(--chip-hue)] hover:bg-[color-mix(in_oklch,var(--chip-hue)_20%,transparent)]",
          className,
        )}
      >
        <Icon className="size-3" aria-hidden />
        <span>{displayLabel}</span>
        {count !== undefined && (
          <span
            className={cn(
              "tabular-nums",
              active ? "text-white/80" : "text-[color:var(--chip-hue)]/60",
            )}
          >
            {count}
          </span>
        )}
      </span>
    );
  }

  return (
    <span
      style={style}
      className={cn(
        base,
        "h-6 rounded-md px-1.5 text-xs font-medium",
        "bg-[color-mix(in_oklch,var(--chip-hue)_12%,transparent)]",
        "text-[color:var(--chip-hue)]",
        className,
      )}
    >
      <Icon className="size-3.5" aria-hidden />
      <span>{displayLabel}</span>
    </span>
  );
}
