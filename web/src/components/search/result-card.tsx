import { useState } from "react";
import { formatDistanceToNow } from "date-fns";
import { ArrowUpRight, Link2 } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { SourceChip } from "@/components/source-chip";
import { sourceMetaFor } from "@/components/source-meta";
import { cn } from "@/lib/utils";
import { EmailCardBody } from "./cards/email";
import { TelegramCardBody } from "./cards/telegram";
import { PaperlessCardBody } from "./cards/paperless";
import { FilesystemCardBody } from "./cards/filesystem";
import { DefaultCardBody } from "./cards/default";
import { RelatedFooter } from "./related-footer";

interface Props {
  hit: DocumentHit;
  onOpenChat: (hit: DocumentHit) => void;
  onDownload: (hit: DocumentHit) => void;
  onNavigateRelated: (doc: DocumentHit) => void;
}

function relative(iso: string): string {
  try {
    return formatDistanceToNow(new Date(iso), { addSuffix: true });
  } catch {
    return iso;
  }
}

export function ResultCard({
  hit,
  onOpenChat,
  onDownload,
  onNavigateRelated,
}: Props) {
  const [relatedOpen, setRelatedOpen] = useState(false);
  const titleText = hit.title || hit.source_id;
  const hasExternal = hit.url && !hit.url.startsWith("file://");
  const relatedCount = hit.related_count ?? 0;
  const sourceLabel = sourceMetaFor(hit.source_type).label;

  // Telegram result cards own their own body layout: match mode
  // renders a pinpoint message row, semantic-fallback renders a
  // bookended window preview. In both cases the stock title/headline
  // section above would duplicate information, so we hide it for
  // telegram and let the body carry the identity chrome.
  const isTelegramCustomBody =
    hit.source_type === "telegram" &&
    (!!hit.match_source_id ||
      Array.isArray(hit.metadata?.message_lines));

  return (
    <article
      className={cn(
        "group rounded-lg border border-border bg-card text-card-foreground",
        "transition-[background-color,border-color] hover:border-accent-foreground/20 hover:bg-card-hover",
      )}
    >
      <div className="flex flex-col gap-2 p-4">
        <div className="flex items-center gap-2 text-[12px] text-muted-foreground">
          <SourceChip
            type={hit.source_type}
            label={`${sourceLabel} · ${hit.source_name}`}
          />
          <time
            className="tabular-nums"
            dateTime={hit.created_at}
            title={hit.created_at}
          >
            {relative(hit.created_at)}
          </time>

          {hasExternal && (
            <a
              href={hit.url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center text-muted-foreground/80 transition-colors hover:text-foreground"
              title="Open original"
            >
              <ArrowUpRight className="size-3.5" aria-hidden />
            </a>
          )}

          {relatedCount > 0 && (
            <button
              type="button"
              onClick={() => setRelatedOpen((v) => !v)}
              aria-expanded={relatedOpen}
              className={cn(
                "ml-auto inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium transition-colors",
                relatedOpen
                  ? "bg-primary/15 text-primary"
                  : "text-primary/90 hover:bg-primary/10",
              )}
            >
              <Link2 className="size-3" aria-hidden strokeWidth={2.5} />
              <span>
                <span className="tabular-nums">{relatedCount}</span> related
              </span>
            </button>
          )}
        </div>

        {!isTelegramCustomBody && (
          <>
            <h3 className="text-[15px] font-medium leading-[1.35] tracking-[-0.005em] text-foreground">
              <span className="line-clamp-2">{titleText}</span>
            </h3>

            {hit.headline ? (
              <p
                className={cn(
                  "line-clamp-2 text-[13.5px] leading-[1.55] text-muted-foreground",
                  "[&_em]:rounded-sm [&_em]:bg-primary/15 [&_em]:px-0.5 [&_em]:font-medium [&_em]:not-italic [&_em]:text-foreground",
                  "[&_mark]:rounded-sm [&_mark]:bg-primary/15 [&_mark]:px-0.5 [&_mark]:font-medium [&_mark]:text-foreground",
                )}
                dangerouslySetInnerHTML={{ __html: hit.headline }}
              />
            ) : hit.content ? (
              <p className="line-clamp-2 text-[13.5px] leading-[1.55] text-muted-foreground">
                {hit.content}
              </p>
            ) : null}
          </>
        )}

        <CardBody
          hit={hit}
          onOpenChat={onOpenChat}
          onDownload={onDownload}
        />
      </div>

      {relatedOpen && relatedCount > 0 && (
        <div className="border-t border-border/70 bg-muted/30 px-4 py-3">
          <RelatedFooter
            docID={hit.id}
            count={relatedCount}
            onNavigate={onNavigateRelated}
          />
        </div>
      )}
    </article>
  );
}

function CardBody({
  hit,
  onOpenChat,
  onDownload,
}: {
  hit: DocumentHit;
  onOpenChat: (hit: DocumentHit) => void;
  onDownload: (hit: DocumentHit) => void;
}) {
  switch (hit.source_type) {
    case "imap":
      return <EmailCardBody hit={hit} />;
    case "telegram":
      return <TelegramCardBody hit={hit} onOpenChat={onOpenChat} />;
    case "paperless":
      return <PaperlessCardBody hit={hit} onDownload={onDownload} />;
    case "filesystem":
      return <FilesystemCardBody hit={hit} />;
    default:
      return <DefaultCardBody hit={hit} />;
  }
}
