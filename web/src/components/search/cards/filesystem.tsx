import { Download } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { Button } from "@/components/ui/button";
import { FileTypeIcon } from "@/components/search/primitives/file-type-icon";
import { SnippetBody } from "@/components/search/primitives/snippet-body";
import { MetaRow } from "@/components/search/primitives/meta-row";

// Filesystem card content region. Deliberately technical and restrained —
// a file on disk, not correspondence. Single-column composition:
//   1. Path header — FileTypeIcon + monospace path; parent dirs muted,
//      slashes very muted, filename bold. The chassis h3 already shows
//      the filename, so this line gives the user the directory context
//      (where in the tree this file lives). Monospace is load-bearing
//      here: it makes the slashes align and the path read as a path.
//   2. Snippet — matched content in a soft neutral wash seeded off
//      --source-filesystem (which is near-neutral cool-gray). No
//      border, no rule — different from the IMAP/Paperless cards'
//      ruled/bordered treatments on purpose.
//   3. Meta footer — monospace extension stamp (distinct visual unit),
//      size, modified time, Download action.
// No avatar, no file tile, no letterhead. The mono-path + neutral-wash
// pairing is the filesystem-specific gesture.

interface Props {
  hit: DocumentHit;
  onDownload?: (hit: DocumentHit) => void;
}

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

function formatRelativeTime(iso: string): string {
  try {
    const d = new Date(iso);
    const ms = Date.now() - d.getTime();
    const days = Math.round(ms / (1000 * 60 * 60 * 24));
    if (days === 0) return "today";
    if (days === 1) return "yesterday";
    if (days < 7) return `${days}d ago`;
    if (days < 30) return `${Math.round(days / 7)}w ago`;
    const sameYear = d.getFullYear() === new Date().getFullYear();
    return d.toLocaleDateString(undefined, {
      month: "short",
      day: "numeric",
      year: sameYear ? undefined : "numeric",
    });
  } catch {
    return iso;
  }
}

export function FilesystemCardBody({ hit, onDownload }: Readonly<Props>) {
  const m = hit.metadata ?? {};
  const path = str(m.path) ?? hit.source_id;
  const extension = str(m.extension) ?? "";
  const mime = hit.mime_type ?? str(m.content_type);
  const rawSize = num(m.size) ?? hit.size;
  const sizeLabel = formatBytes(rawSize);
  const modLabel = hit.created_at ? formatRelativeTime(hit.created_at) : null;
  const hasSnippet = !!(hit.headline || hit.content);
  const canDownload = !!onDownload;

  const segments = path.split("/").filter(Boolean);
  const filename = segments.pop() ?? path;
  const dirs = segments;
  const extBadge = extension.replace(/^\./, "").toUpperCase();

  return (
    <div className="mt-2.5 flex flex-col gap-3">
      <div className="flex min-w-0 items-center gap-2">
        <FileTypeIcon
          mime={mime}
          extension={extension}
          className="size-4 shrink-0 text-muted-foreground/70"
          strokeWidth={1.75}
        />
        <span
          className="min-w-0 truncate font-mono text-[12.5px] leading-tight"
          title={path}
        >
          {dirs.map((d, i) => (
            <span key={`${d}-${i}`}>
              <span className="text-muted-foreground/75">{d}</span>
              <span className="text-muted-foreground/35">/</span>
            </span>
          ))}
          <span className="font-semibold text-foreground">{filename}</span>
        </span>
      </div>

      {hasSnippet && (
        <div
          style={{
            backgroundColor:
              "color-mix(in oklch, var(--source-filesystem) 16%, transparent)",
          }}
          className="rounded-md px-3 py-2"
        >
          <SnippetBody hit={hit} lineClamp={3} />
        </div>
      )}

      {(extBadge || sizeLabel || modLabel || canDownload) && (
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            {extBadge && (
              <span
                style={{
                  backgroundColor:
                    "color-mix(in oklch, var(--source-filesystem) 22%, transparent)",
                  color:
                    "color-mix(in oklch, var(--source-filesystem) 60%, var(--foreground))",
                }}
                className="inline-flex h-5 items-center rounded px-1.5 font-mono text-[10.5px] font-semibold uppercase tracking-wider leading-none"
              >
                {extBadge}
              </span>
            )}
            <MetaRow
              items={[
                sizeLabel && {
                  key: "size",
                  label: sizeLabel,
                  numeric: true,
                },
                modLabel && {
                  key: "mod",
                  label: modLabel,
                  numeric: true,
                },
              ]}
            />
          </div>
          {canDownload && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onDownload?.(hit)}
              className="h-7 shrink-0 gap-1.5 rounded-md px-2 text-[12.5px] font-medium"
            >
              <Download className="size-3.5" aria-hidden />
              Download
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
