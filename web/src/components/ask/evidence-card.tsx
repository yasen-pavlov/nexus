import type { CSSProperties } from "react";
import { ArrowUpRight } from "lucide-react";

import { InlineImage } from "@/components/conversation/inline-media";
import { mimeIsImage } from "@/components/conversation/mime-helpers";
import { sourceMetaFor } from "@/components/source-meta";
import { SourceChip } from "@/components/source-chip";
import { SourceCardBody } from "@/components/search/source-card-body";
import { cardOwnsSnippet } from "@/components/search/card-snippet";
import { chunkPreviewToHit } from "@/components/search/chunk-to-hit";
import type { ChunkPreview } from "@/lib/api-types";
import { sanitizeHighlight } from "@/lib/sanitize-highlight";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

export interface EvidenceCardProps {
  /** 1-based index. Matches CitationPill numbering. */
  number: number;
  chunk: ChunkPreview;
  flashed?: boolean;
  /** Toggle/flash this source (the number badge is the citation handle). */
  onActivate: () => void;
  /** Open/download the whole document (paperless, filesystem, attachment). */
  onDownload?: (chunk: ChunkPreview) => void;
  /** Download a named email attachment. */
  onAttachmentDownload?: (att: { id: string; filename: string }) => void;
}

/**
 * One retrieved-chunk card in the Ask evidence rail — now the SAME rich
 * per-source card as search (Paperless letterhead, calendar tile, filesystem
 * path, …) via a ChunkPreview→DocumentHit adapter + the shared SourceCardBody.
 *
 * Composition avoids nested interactives: the outer element is a plain <div>
 * (the flash/scroll target carrying data-chunk-id), the number badge is the
 * only activate <button>, and the title link, "open original" link, the
 * SourceCardBody (which owns its Download/attachment buttons) and the
 * InlineImage thumbnail all render as SIBLINGS — never inside a button.
 *
 * Open affordances mirror search: a title/icon link to `url` for url-bearing
 * sources (e.g. Paperless → its web UI) and the body's Download for
 * binary-backed sources (filesystem, attachments). `flashed` fades a hue-keyed
 * ring after the matching CitationPill is clicked.
 */
export function EvidenceCard({
  number,
  chunk,
  flashed,
  onActivate,
  onDownload,
  onAttachmentDownload,
}: Readonly<EvidenceCardProps>) {
  const meta = sourceMetaFor(chunk.source);
  const hueStyle = {
    "--chip-hue": `var(${meta.colorVar})`,
  } as CSSProperties;
  const isImage = mimeIsImage(chunk.mime_type);
  const hit = chunkPreviewToHit(chunk);
  const hasExternal = !!chunk.url && !chunk.url.startsWith("file://");
  const ownsSnippet = cardOwnsSnippet(chunk.source);
  const titleText = chunk.title || chunk.id;

  return (
    <div
      data-chunk-id={chunk.id}
      style={hueStyle}
      className={cn(
        "group relative w-full overflow-hidden rounded-lg border border-border bg-card text-left",
        "transition-[background-color,border-color,box-shadow] duration-150",
        "hover:bg-card-hover hover:border-accent-foreground/20",
        // Flash uses a hue-keyed ring that transitions in/out.
        flashed
          ? "ring-2 ring-offset-2 ring-offset-background ring-[color:var(--chip-hue)]"
          : "ring-0 ring-offset-0",
      )}
    >
      <span
        aria-hidden
        style={{ background: `var(${meta.colorVar}, var(--source-default))` }}
        className="absolute inset-y-2 left-0 w-[3px] rounded-r-full opacity-70"
      />
      <div className="flex min-w-0 flex-col gap-1.5 p-3.5 pl-5">
        <div className="flex items-center gap-2">
          <button
            type="button"
            data-chunk-activate={chunk.id}
            onClick={onActivate}
            aria-label={`Toggle source ${number}`}
            className={cn(
              "inline-flex h-5 min-w-5 items-center justify-center rounded-md px-1 text-[12px] font-semibold tabular-nums leading-none",
              "bg-[color-mix(in_oklch,var(--chip-hue)_14%,transparent)] text-[color:var(--chip-hue)]",
              "transition-colors hover:bg-[color-mix(in_oklch,var(--chip-hue)_22%,transparent)]",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
            )}
          >
            {number}
          </button>
          <SourceChip type={chunk.source} variant="default" />
          <div className="ml-auto flex items-center gap-2 text-[11px] text-muted-foreground">
            {chunk.date ? formatRelative(chunk.date) : ""}
            {hasExternal && (
              <a
                href={chunk.url}
                target="_blank"
                rel="noopener noreferrer"
                title="Open original"
                className="inline-flex items-center text-muted-foreground/80 transition-colors hover:text-foreground"
              >
                <ArrowUpRight className="size-3.5" aria-hidden />
              </a>
            )}
          </div>
        </div>

        {hasExternal ? (
          <a
            href={chunk.url}
            target="_blank"
            rel="noopener noreferrer"
            title="Open original"
            className="line-clamp-2 text-[14px] font-medium leading-[20px] text-foreground transition-colors hover:text-primary"
          >
            {titleText}
          </a>
        ) : (
          <div className="line-clamp-2 text-[14px] font-medium leading-[20px] text-foreground">
            {titleText}
          </div>
        )}

        {!ownsSnippet && chunk.headline && (
          <p
            className={cn(
              "line-clamp-3 text-[12.5px] leading-[18px] text-muted-foreground",
              "[&_em]:rounded-sm [&_em]:bg-primary/15 [&_em]:px-0.5 [&_em]:font-medium [&_em]:not-italic [&_em]:text-foreground",
              "[&_mark]:rounded-sm [&_mark]:bg-primary/15 [&_mark]:px-0.5 [&_mark]:font-medium [&_mark]:text-foreground",
            )}
            dangerouslySetInnerHTML={{ __html: sanitizeHighlight(chunk.headline) }}
          />
        )}

        <SourceCardBody
          hit={hit}
          onDownload={onDownload ? () => onDownload(chunk) : undefined}
          onAttachmentDownload={onAttachmentDownload}
        />
      </div>

      {/* Inline thumbnail for image chunks — a sibling (not nested) since the
          body / activate control already own the interactive elements. */}
      {isImage && (
        <div className="px-5 pb-3.5">
          <InlineImage id={chunk.id} filename={titleText} />
        </div>
      )}
    </div>
  );
}
