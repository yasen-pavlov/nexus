import { Download } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { Button } from "@/components/ui/button";
import { SnippetBody } from "@/components/search/primitives/snippet-body";
import { MetaRow } from "@/components/search/primitives/meta-row";
import { TagPill } from "@/components/search/primitives/tag-pill";

// Paperless card content region. Composition intent: "filed correspondence".
//   1. Letterhead panel (signature move) — a paperless-tinted rectangle
//      that reads like the head of a formal filed document. Tiny uppercase
//      "CORRESPONDENT" label, then the sender organization name prominent.
//      Document type sits on the right as a classification stamp
//      (outlined small-caps box, not a chip — reads as "archival marking"
//      rather than "UI tag").
//   2. Snippet — plain paragraph of matched text, no framing. Deliberate
//      restraint so the card doesn't mimic the IMAP cards' bordered/ruled
//      snippet boxes.
//   3. Tag shelf — seeded-hue TagPill row.
//   4. Meta footer — filed date, size, download action.
// Horizontal letterhead band is the Paperless-specific gesture. Avoids
// the IMAP cards' round avatars and tall file tiles on purpose.

interface Props {
  hit: DocumentHit;
  onDownload?: (hit: DocumentHit) => void;
}

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

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      month: "short",
      day: "numeric",
      year: "numeric",
    });
  } catch {
    return iso;
  }
}

export function PaperlessCardBody({ hit, onDownload }: Readonly<Props>) {
  const m = hit.metadata ?? {};
  const correspondent = str(m.correspondent);
  const docType = str(m.document_type);
  const tags = strArr(m.tags);
  const filedRaw = str(m.added);
  const sizeLabel = formatBytes(hit.size);
  const hasSnippet = !!(hit.headline || hit.content);
  const canDownload = !!onDownload;
  const filedLabel = filedRaw ? formatDate(filedRaw) : null;

  if (
    !correspondent &&
    !docType &&
    !hasSnippet &&
    tags.length === 0 &&
    !sizeLabel &&
    !filedLabel
  ) {
    return null;
  }

  return (
    <div className="mt-2.5 flex flex-col gap-3">
      {correspondent && (
        <div
          style={{
            backgroundColor:
              "color-mix(in oklch, var(--source-paperless) 6%, transparent)",
            borderColor:
              "color-mix(in oklch, var(--source-paperless) 22%, var(--border))",
          }}
          className="flex items-center justify-between gap-3 rounded-md border px-3 py-2"
        >
          <div className="min-w-0 flex-1">
            <div
              style={{
                color:
                  "color-mix(in oklch, var(--source-paperless) 60%, var(--muted-foreground))",
              }}
              className="text-[9.5px] font-semibold uppercase tracking-[0.14em] leading-none"
            >
              Correspondent
            </div>
            <div
              className="mt-1 truncate text-[14px] font-semibold leading-tight text-foreground"
              title={correspondent}
            >
              {correspondent}
            </div>
          </div>
          {docType && (
            <span
              style={{
                color:
                  "color-mix(in oklch, var(--source-paperless) 70%, var(--foreground))",
                borderColor:
                  "color-mix(in oklch, var(--source-paperless) 38%, var(--border))",
                backgroundColor:
                  "color-mix(in oklch, var(--source-paperless) 8%, transparent)",
              }}
              className="shrink-0 rounded-sm border px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.12em] leading-none"
              title={docType}
            >
              {docType}
            </span>
          )}
        </div>
      )}

      {hasSnippet && (
        <div className="px-0.5">
          <SnippetBody hit={hit} lineClamp={3} />
        </div>
      )}

      {tags.length > 0 && (
        <div className="flex flex-wrap items-center gap-1 px-0.5">
          {tags.map((t) => (
            <TagPill key={t} label={t} />
          ))}
        </div>
      )}

      {(filedLabel || sizeLabel || canDownload) && (
        <div className="flex items-center justify-between gap-2 px-0.5">
          <MetaRow
            items={[
              filedLabel && {
                key: "filed",
                label: (
                  <span>
                    <span className="text-muted-foreground/60">Filed </span>
                    {filedLabel}
                  </span>
                ),
                numeric: true,
              },
              sizeLabel && {
                key: "size",
                label: sizeLabel,
                numeric: true,
              },
            ]}
          />
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
