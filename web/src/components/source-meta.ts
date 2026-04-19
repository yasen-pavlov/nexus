import { Cable, FileText, Folder, Inbox, MessageCircle } from "lucide-react";
import type { ComponentType, SVGProps } from "react";

type IconComponent = ComponentType<SVGProps<SVGSVGElement>>;

export interface SourceMeta {
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
