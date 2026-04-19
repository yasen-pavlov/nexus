import type { CSSProperties } from "react";

import { cn } from "@/lib/utils";
import { ConnectorLogo } from "./connector-logo";
import { connectorTypeLabel } from "./connector-labels";

export type ConnectorTypeKey = "filesystem" | "imap" | "paperless" | "telegram";

const TYPE_ORDER: ConnectorTypeKey[] = ["filesystem", "imap", "paperless", "telegram"];

const TYPE_TAGLINE: Record<ConnectorTypeKey, string> = {
  filesystem: "Notes, markdown, any local files",
  imap: "Mailboxes over IMAP",
  paperless: "Scanned documents & OCR",
  telegram: "Private chats, channels, media",
};

/**
 * The "pick an instrument off the wall" moment. Four tonal tiles with
 * brand logos; tonal wash on hover; marmalade outline + "Selected" pill
 * when active. Primary CTA of the create sheet.
 */
export function ConnectorTypePicker({
  value,
  onChange,
  disabled,
}: {
  value: ConnectorTypeKey | null;
  onChange: (v: ConnectorTypeKey) => void;
  disabled?: boolean;
}) {
  return (
    <fieldset
      className="grid grid-cols-2 gap-3"
      disabled={disabled}
      aria-label="Connector type"
    >
      {TYPE_ORDER.map((t) => {
        const active = value === t;
        return (
          <button
            key={t}
            type="button"
            onClick={() => onChange(t)}
            aria-pressed={active}
            className={cn(
              "group relative flex flex-col items-start gap-3 overflow-hidden rounded-xl border border-border bg-card p-4 text-left",
              "transition-all duration-200",
              "hover:border-accent-foreground/25 hover:bg-card-hover",
              active && "border-primary/60 bg-card-hover ring-1 ring-primary/40",
              disabled && "pointer-events-none opacity-60",
            )}
            style={{ "--chip-hue": `var(--source-${t})` } as CSSProperties}
          >
            <span
              aria-hidden
              className={cn(
                "pointer-events-none absolute inset-0 rounded-[inherit] opacity-0 transition-opacity duration-300",
                "group-hover:opacity-100",
                active && "opacity-100",
              )}
              style={{
                background:
                  "radial-gradient(120% 120% at 0% 0%, color-mix(in oklch, var(--chip-hue) 15%, transparent), transparent 60%)",
              }}
            />
            <div className="relative flex w-full items-center justify-between">
              <ConnectorLogo type={t} size="lg" />
              {active && (
                <span className="rounded-full bg-primary/90 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-primary-foreground">
                  Selected
                </span>
              )}
            </div>
            <div className="relative">
              <div className="text-[14px] font-medium text-foreground">
                {connectorTypeLabel(t)}
              </div>
              <div className="mt-0.5 text-[12px] text-muted-foreground">
                {TYPE_TAGLINE[t]}
              </div>
            </div>
          </button>
        );
      })}
    </fieldset>
  );
}
