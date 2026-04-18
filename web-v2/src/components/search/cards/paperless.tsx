import { Download, FileText } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { Badge } from "@/components/ui/badge";
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

  const hasLetterhead = correspondent || docType;

  return (
    <div className="mt-2 flex flex-col gap-2 text-sm">
      {hasLetterhead && (
        <div className="flex items-baseline gap-1.5 text-muted-foreground">
          <FileText
            className="size-3.5 shrink-0 translate-y-0.5"
            aria-hidden
          />
          {correspondent && (
            <span className="truncate font-medium text-foreground">
              {correspondent}
            </span>
          )}
          {correspondent && docType && (
            <span className="shrink-0 text-muted-foreground/60">·</span>
          )}
          {docType && <span className="truncate">{docType}</span>}
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-1">
          {tags.map((t) => (
            <Badge key={t} variant="secondary" className="font-normal">
              {t}
            </Badge>
          ))}
        </div>

        <div className="flex shrink-0 items-center gap-2">
          {size && (
            <span className="text-xs tabular-nums text-muted-foreground">
              {size}
            </span>
          )}
          {onDownload && hit.mime_type && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 px-2"
              onClick={() => onDownload(hit)}
            >
              <Download className="mr-1 size-3.5" aria-hidden />
              Download
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
