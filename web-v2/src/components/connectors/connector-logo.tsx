import type { CSSProperties, ReactNode } from "react";
import { siTelegram, siPaperlessngx } from "simple-icons";
import { Folder, Inbox, Cable, type LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";

const PLATE_SIZE = { sm: 28, md: 40, lg: 56, xl: 72 } as const;

const SOURCE_VAR: Record<string, string> = {
  imap: "--source-imap",
  telegram: "--source-telegram",
  paperless: "--source-paperless",
  filesystem: "--source-filesystem",
};

function BrandSVG({ path, title, className }: { path: string; title: string; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" role="img" aria-label={title} className={className} fill="currentColor">
      <path d={path} />
    </svg>
  );
}

function LucideMark({ Icon, className }: { Icon: LucideIcon; className?: string }) {
  return <Icon className={className} strokeWidth={1.6} />;
}

export interface ConnectorLogoProps {
  type: string;
  size?: keyof typeof PLATE_SIZE;
  /** Mute the tonal plate — useful on dense list rows */
  quiet?: boolean;
  className?: string;
}

export function ConnectorLogo({ type, size = "md", quiet = false, className }: ConnectorLogoProps) {
  const dim = PLATE_SIZE[size];
  const hueVar = SOURCE_VAR[type] ?? "--source-default";
  const plateOpacity = quiet ? 10 : 14;

  const mark: ReactNode = (() => {
    const cls = "h-[55%] w-[55%]";
    switch (type) {
      case "telegram":
        return <BrandSVG path={siTelegram.path} title={siTelegram.title} className={cls} />;
      case "paperless":
        return <BrandSVG path={siPaperlessngx.path} title={siPaperlessngx.title} className={cls} />;
      case "imap":
        return <LucideMark Icon={Inbox} className={cls} />;
      case "filesystem":
        return <LucideMark Icon={Folder} className={cls} />;
      default:
        return <LucideMark Icon={Cable} className={cls} />;
    }
  })();

  return (
    <div
      style={
        {
          width: dim,
          height: dim,
          "--chip-hue": `var(${hueVar})`,
          backgroundColor: `color-mix(in oklch, var(--chip-hue) ${plateOpacity}%, transparent)`,
          color: `var(--chip-hue)`,
        } as CSSProperties
      }
      className={cn(
        "relative flex shrink-0 items-center justify-center rounded-lg",
        "ring-1 ring-[color:color-mix(in_oklch,var(--chip-hue)_18%,transparent)]",
        "transition-[background-color,box-shadow] duration-200",
        className,
      )}
      aria-hidden
    >
      {mark}
      {size === "xl" && (
        <div
          className="pointer-events-none absolute -inset-px rounded-[inherit]"
          style={{
            background:
              "linear-gradient(135deg, color-mix(in oklch, var(--chip-hue) 22%, transparent) 0%, transparent 55%)",
          }}
        />
      )}
    </div>
  );
}

export function connectorTypeLabel(type: string): string {
  switch (type) {
    case "filesystem":
      return "Filesystem";
    case "imap":
      return "Email · IMAP";
    case "paperless":
      return "Paperless-ngx";
    case "telegram":
      return "Telegram";
    default:
      return type;
  }
}
