import { Download } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { Button } from "@/components/ui/button";

function str(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() ? v : undefined;
}

function strArr(v: unknown): string[] {
  return Array.isArray(v)
    ? v.filter((x): x is string => typeof x === "string")
    : [];
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

interface Props {
  hit: DocumentHit;
  onDownload?: (hit: DocumentHit) => void;
}

export function PaperlessCardBody({ hit, onDownload }: Props) {
  const m = hit.metadata ?? {};
  const correspondent = str(m.correspondent);
  const docType = str(m.document_type);
  const tags = strArr(m.tags);
  const size = formatBytes(hit.size);
  const canDownload = !!onDownload && !!hit.mime_type;

  const hasLetterhead = !!(correspondent || docType);
  const hasMetaRow = tags.length > 0 || !!size || canDownload;

  if (!hasLetterhead && !hasMetaRow) return null;

  return (
    <div className="mt-2 flex flex-col gap-2 text-[13px]">
      {hasLetterhead && (
        <div className="flex min-w-0 items-baseline gap-1.5 text-muted-foreground">
          {correspondent && (
            <span className="truncate font-medium text-foreground/90">
              {correspondent}
            </span>
          )}
          {correspondent && docType && (
            <span className="shrink-0 text-muted-foreground/50">·</span>
          )}
          {docType && <span className="truncate">{docType}</span>}
        </div>
      )}

      {hasMetaRow && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          {tags.length > 0 ? (
            <div className="flex min-w-0 flex-wrap items-center gap-1">
              {tags.map((t) => (
                <span
                  key={t}
                  className="inline-flex h-5 shrink-0 items-center rounded-full border border-border bg-muted/40 px-2 text-[11.5px] font-medium text-muted-foreground"
                >
                  {t}
                </span>
              ))}
            </div>
          ) : (
            <span />
          )}

          <div className="flex shrink-0 items-center gap-2">
            {size && (
              <span className="text-[12px] tabular-nums text-muted-foreground">
                {size}
              </span>
            )}
            {canDownload && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => onDownload?.(hit)}
                className="h-7 gap-1.5 rounded-md px-2 text-[12.5px] font-medium"
              >
                <Download className="size-3.5" aria-hidden />
                Download
              </Button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
