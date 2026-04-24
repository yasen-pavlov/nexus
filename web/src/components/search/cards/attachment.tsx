import { Paperclip } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { FileTypeIcon } from "@/components/search/primitives/file-type-icon";
import { SnippetBody } from "@/components/search/primitives/snippet-body";
import { MetaRow } from "@/components/search/primitives/meta-row";

// Attachment card content region. Two-column composition — the inverse
// rhythm of the email card's round-avatar + horizontal flow:
//   Left: a tall document tile — tinted rectangle with the file-type
//     glyph and a mime/extension stamp band at the bottom. Substantial
//     enough that "this is a file" registers in one glance. The tile
//     itself is the download affordance when onDownload is wired — no
//     separate Download button needed.
//   Right: a stack of
//     1. Provenance — "Attached to <parent subject>" prefixed with a
//        paperclip. The attachment has no life without its parent.
//     2. Snippet — Tika-extracted excerpt in a soft IMAP-tinted box.
//        Box shape (full border) differs from the email card's
//        left-rule so the two IMAP cards don't look like copies.
//     3. Meta footer — size.
// The mime/extension already lives on the tile stamp, so the meta row
// stays lean.

interface Props {
  hit: DocumentHit;
  onDownload?: (hit: DocumentHit) => void;
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() ? v : undefined;
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

function extOf(name: string | undefined): string {
  if (!name) return "";
  const i = name.lastIndexOf(".");
  return i > 0 ? name.slice(i + 1) : "";
}

// Exact-match table. Simple lookup kept separate from the fuzzy
// substring/prefix rules below so the happy path stays one branch.
const MIME_EXACT: Record<string, string> = {
  "application/pdf": "PDF",
  "application/zip": "ZIP",
  "application/gzip": "GZIP",
  "application/x-7z-compressed": "7Z",
  "application/x-tar": "TAR",
  "application/json": "JSON",
  "application/xml": "XML",
  "application/msword": "DOC",
  "application/vnd.ms-excel": "XLS",
  "application/vnd.ms-powerpoint": "PPT",
  "text/plain": "TEXT",
  "text/csv": "CSV",
  "text/html": "HTML",
  "text/markdown": "MD",
};

// Prefix/substring rules. Order matters — images/audio/video are
// prefix matches, Office substrings are substring matches.
const MIME_FUZZY: ReadonlyArray<{ test: (m: string) => boolean; label: string | ((m: string) => string) }> = [
  { test: (m) => m.startsWith("image/"), label: (m) => m.slice(6).toUpperCase() },
  { test: (m) => m.startsWith("audio/"), label: "AUDIO" },
  { test: (m) => m.startsWith("video/"), label: "VIDEO" },
  { test: (m) => m.includes("wordprocessingml"), label: "DOC" },
  { test: (m) => m.includes("spreadsheetml"), label: "XLS" },
  { test: (m) => m.includes("presentationml"), label: "PPT" },
];

// Short display label for the tile stamp. Falls back to the filename
// extension when mime is generic (octet-stream is a common Tika miscast).
function mimeStamp(mime: string | undefined, fallbackExt: string): string {
  const m = (mime ?? "").toLowerCase();
  const exact = MIME_EXACT[m];
  if (exact) return exact;
  for (const rule of MIME_FUZZY) {
    if (rule.test(m)) {
      return typeof rule.label === "string" ? rule.label : rule.label(m);
    }
  }
  return fallbackExt.toUpperCase().slice(0, 6);
}

export function AttachmentCardBody({ hit, onDownload }: Readonly<Props>) {
  const m = hit.metadata ?? {};
  const parentSubject = str(m.parent_subject);
  const filename = str(m.filename) ?? hit.title;
  const ext = extOf(filename);
  const mime = hit.mime_type ?? str(m.content_type);
  const stamp = mimeStamp(mime, ext);
  const sizeLabel = formatBytes(hit.size);
  const hasSnippet = !!(hit.headline || hit.content);
  const canDownload = !!onDownload;

  return (
    <div className="mt-2.5 flex items-start gap-3">
      {canDownload ? (
        <button
          type="button"
          onClick={() => onDownload?.(hit)}
          title={`Download ${filename}`}
          style={{
            backgroundColor:
              "color-mix(in oklch, var(--source-imap) 7%, transparent)",
            borderColor:
              "color-mix(in oklch, var(--source-imap) 25%, var(--border))",
          }}
          className="group/tile flex h-[74px] w-14 shrink-0 cursor-pointer flex-col overflow-hidden rounded-md border transition-colors hover:bg-[color-mix(in_oklch,var(--source-imap)_14%,transparent)]"
        >
          <div className="flex flex-1 items-center justify-center">
            <FileTypeIcon
              mime={mime}
              extension={ext}
              className="size-6 text-[color:var(--source-imap)] transition-transform group-hover/tile:scale-110"
              strokeWidth={1.5}
            />
          </div>
          {stamp && (
            <div
              style={{
                backgroundColor:
                  "color-mix(in oklch, var(--source-imap) 14%, transparent)",
                color:
                  "color-mix(in oklch, var(--source-imap) 75%, var(--foreground))",
                borderTopColor:
                  "color-mix(in oklch, var(--source-imap) 20%, var(--border))",
              }}
              className="border-t px-1 py-0.5 text-center text-[9px] font-semibold uppercase tracking-[0.08em] leading-[1.3]"
            >
              {stamp}
            </div>
          )}
        </button>
      ) : (
        <div
          style={{
            backgroundColor:
              "color-mix(in oklch, var(--source-imap) 7%, transparent)",
            borderColor:
              "color-mix(in oklch, var(--source-imap) 25%, var(--border))",
          }}
          className="flex h-[74px] w-14 shrink-0 flex-col overflow-hidden rounded-md border"
        >
          <div className="flex flex-1 items-center justify-center">
            <FileTypeIcon
              mime={mime}
              extension={ext}
              className="size-6 text-[color:var(--source-imap)]"
              strokeWidth={1.5}
            />
          </div>
          {stamp && (
            <div
              style={{
                backgroundColor:
                  "color-mix(in oklch, var(--source-imap) 14%, transparent)",
                color:
                  "color-mix(in oklch, var(--source-imap) 75%, var(--foreground))",
                borderTopColor:
                  "color-mix(in oklch, var(--source-imap) 20%, var(--border))",
              }}
              className="border-t px-1 py-0.5 text-center text-[9px] font-semibold uppercase tracking-[0.08em] leading-[1.3]"
            >
              {stamp}
            </div>
          )}
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col gap-2">
        {parentSubject && (
          <div className="flex min-w-0 items-center gap-1.5 text-[12px]">
            <Paperclip
              className="size-3 shrink-0 text-[color:var(--source-imap)]/70"
              aria-hidden
            />
            <span className="shrink-0 text-[10.5px] uppercase tracking-wide text-muted-foreground/70">
              Attached to
            </span>
            <span
              className="truncate text-[12.5px] font-medium text-foreground/90"
              title={parentSubject}
            >
              {parentSubject}
            </span>
          </div>
        )}

        {hasSnippet && (
          <div
            style={{
              backgroundColor:
                "color-mix(in oklch, var(--source-imap) 3%, transparent)",
              borderColor:
                "color-mix(in oklch, var(--source-imap) 18%, var(--border))",
            }}
            className="rounded-md border px-3 py-2"
          >
            <SnippetBody hit={hit} lineClamp={3} />
          </div>
        )}

        <MetaRow
          items={[
            sizeLabel && {
              key: "size",
              label: sizeLabel,
              numeric: true,
            },
          ]}
        />
      </div>
    </div>
  );
}
