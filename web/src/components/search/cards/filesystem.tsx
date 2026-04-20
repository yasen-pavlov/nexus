import { Folder } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";

function str(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() ? v : undefined;
}

function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

function formatBytes(n?: number): string {
  if (!n || n <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

export function FilesystemCardBody({ hit }: Readonly<{ hit: DocumentHit }>) {
  const m = hit.metadata ?? {};
  const path = str(m.path) ?? hit.source_id;
  const extension = str(m.extension);
  const size = formatBytes(num(m.size) ?? hit.size);

  const segments = path.split("/").filter(Boolean);
  const file = segments.pop() ?? path;
  const dirs = segments;

  return (
    <div className="mt-2 flex items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-1.5">
        <Folder
          className="size-3.5 shrink-0 text-muted-foreground/70"
          aria-hidden
        />
        <span className="truncate font-mono text-[12.5px] leading-tight">
          {dirs.map((d, i) => (
            <span key={`${d}-${i}`}>
              <span className="text-muted-foreground">{d}</span>
              <span className="text-muted-foreground/40">/</span>
            </span>
          ))}
          <span className="text-foreground">{file}</span>
        </span>
      </div>

      <div className="flex shrink-0 items-center gap-2 text-[12px] text-muted-foreground">
        {extension && (
          <span className="rounded border border-border bg-muted/40 px-1.5 py-0.5 font-mono text-[10.5px] font-medium uppercase tracking-wide">
            {extension.replace(/^\./, "")}
          </span>
        )}
        {size && <span className="tabular-nums">{size}</span>}
      </div>
    </div>
  );
}
