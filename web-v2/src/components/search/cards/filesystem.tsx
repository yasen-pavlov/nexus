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

export function FilesystemCardBody({ hit }: { hit: DocumentHit }) {
  const m = hit.metadata ?? {};
  const path = str(m.path) ?? hit.source_id;
  const extension = str(m.extension);
  const size = formatBytes(num(m.size) ?? hit.size);

  const segments = path.split("/").filter(Boolean);
  const file = segments.pop();
  const dirs = segments;

  return (
    <div className="mt-2 flex items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-1.5 font-mono text-xs text-muted-foreground">
        <Folder className="size-3.5 shrink-0" aria-hidden />
        <span className="truncate">
          {dirs.map((d, i) => (
            <span key={`${d}-${i}`}>
              {d}
              <span className="text-muted-foreground/40">/</span>
            </span>
          ))}
          {file && <span className="text-foreground">{file}</span>}
        </span>
      </div>

      <div className="flex shrink-0 items-center gap-2 text-xs tabular-nums text-muted-foreground">
        {extension && (
          <span className="rounded border border-border/80 px-1.5 py-0.5 font-mono uppercase">
            {extension.replace(/^\./, "")}
          </span>
        )}
        {size && <span>{size}</span>}
      </div>
    </div>
  );
}
