import type { ReactNode } from "react";
import { Paperclip } from "lucide-react";

function formatBytes(n?: number): string | undefined {
  if (!n || n <= 0) return undefined;
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return `${v < 10 && u > 0 ? v.toFixed(1) : Math.round(v)} ${units[u]}`;
}

interface ChipProps {
  filename: string;
  size?: number;
  onDownload: () => void;
}

export function AttachmentChip({ filename, size, onDownload }: ChipProps) {
  const sizeLabel = formatBytes(size);
  return (
    <button
      type="button"
      onClick={onDownload}
      className="group inline-flex max-w-full items-center gap-1.5 rounded-md border border-border/80 bg-card px-2.5 py-1 text-[12.5px] transition-colors hover:border-accent-foreground/25 hover:bg-card-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
    >
      <Paperclip className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
      <span className="truncate font-medium text-foreground/90">{filename}</span>
      {sizeLabel && (
        <span className="shrink-0 tabular-nums text-muted-foreground/80">
          · {sizeLabel}
        </span>
      )}
    </button>
  );
}

interface RowProps {
  children: ReactNode;
}

export function AttachmentChipRow({ children }: RowProps) {
  return <div className="mt-2 flex flex-wrap gap-1.5">{children}</div>;
}
