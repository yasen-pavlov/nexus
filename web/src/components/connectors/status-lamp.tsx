import { cn } from "@/lib/utils";
import type { ConnectorStatus } from "./status";

const LAMP_COLOR: Record<ConnectorStatus, string> = {
  idle: "var(--muted-foreground)",
  running: "var(--primary)",
  succeeded: "oklch(0.70 0.12 155)",
  failed: "var(--destructive)",
  canceled: "var(--muted-foreground)",
  // Muted amber — distinct from destructive red; signals "we didn't get
  // to finish" rather than "the source rejected us".
  interrupted: "oklch(0.72 0.10 70)",
  disabled: "color-mix(in oklch, var(--muted-foreground) 50%, transparent)",
};

const STATUS_LABEL: Record<ConnectorStatus, string> = {
  idle: "Idle",
  running: "Syncing",
  succeeded: "Last sync OK",
  failed: "Last sync failed",
  canceled: "Canceled",
  interrupted: "Interrupted",
  disabled: "Disabled",
};

export function StatusLamp({
  status,
  size = 8,
  className,
}: {
  status: ConnectorStatus;
  size?: number;
  className?: string;
}) {
  return (
    <span
      role="status"
      aria-label={status}
      style={{
        width: size,
        height: size,
        backgroundColor: LAMP_COLOR[status],
        color: LAMP_COLOR[status],
      }}
      className={cn(
        "inline-block shrink-0 rounded-full",
        status === "running" && "lamp-breathe",
        className,
      )}
    />
  );
}

export function StatusChip({
  status,
  hint,
  className,
}: {
  status: ConnectorStatus;
  hint?: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 rounded-full border border-border bg-card px-2.5 py-1",
        "text-[11.5px] font-medium tabular-nums text-muted-foreground",
        className,
      )}
    >
      <StatusLamp status={status} />
      <span className="text-foreground/80">{STATUS_LABEL[status]}</span>
      {hint && <span className="text-muted-foreground">· {hint}</span>}
    </span>
  );
}
