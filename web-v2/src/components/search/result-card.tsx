import { formatDistanceToNow } from "date-fns";
import { ArrowUpRight } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
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

// Compact relative date: "3d", "12h", "2mo", "1y" — fits a 5ch mono gutter
// without needing truncation. Full phrase goes in the title attribute.
function compactRelative(iso: string): string {
  try {
    const t = new Date(iso).getTime();
    if (Number.isNaN(t)) return iso;
    const diffMs = Date.now() - t;
    const sec = Math.max(1, Math.floor(diffMs / 1000));
    const min = Math.floor(sec / 60);
    const hr = Math.floor(min / 60);
    const day = Math.floor(hr / 24);
    const mo = Math.floor(day / 30);
    const yr = Math.floor(day / 365);
    if (sec < 60) return `${sec}s`;
    if (min < 60) return `${min}m`;
    if (hr < 24) return `${hr}h`;
    if (day < 30) return `${day}d`;
    if (mo < 12) return `${mo}mo`;
    return `${yr}y`;
  } catch {
    return iso;
  }
}

function fullDate(iso: string): string {
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
  const titleText = hit.title || hit.source_id;
  const hasExternal = hit.url && !hit.url.startsWith("file://");
  const relative = compactRelative(hit.created_at);
  const absolute = fullDate(hit.created_at);

  return (
    <article
      className={cn(
        // No card border — just a rule separating rows. Density goes up.
        "group relative grid grid-cols-[4.5rem_1fr] gap-x-4 border-b border-border/60 py-4 transition-colors",
        "hover:bg-accent/30",
      )}
    >
      {/* Leading mono timestamp gutter — the log-tail signature. */}
      <div
        className="pt-0.5 font-mono text-xs tabular-nums text-muted-foreground"
        title={absolute}
      >
        {relative}
      </div>

      <div className="min-w-0">
        {/* Source handle — dim mono above the title, @type·connector */}
        <div className="mb-0.5 flex items-center gap-1 font-mono text-[10px] uppercase tracking-wider text-muted-foreground/70">
          <span className="text-muted-foreground/50">@</span>
          <span>{hit.source_type}</span>
          <span className="text-muted-foreground/40">·</span>
          <span className="normal-case tracking-normal">{hit.source_name}</span>
        </div>

        {/* Title: the type itself is the structure, no outer box. */}
        <h3 className="text-[17px] font-medium leading-snug tracking-[-0.005em]">
          {hasExternal ? (
            <a
              href={hit.url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-baseline gap-1 hover:underline"
            >
              <span className="line-clamp-2">{titleText}</span>
              <ArrowUpRight
                className="inline size-3.5 shrink-0 translate-y-0.5 text-muted-foreground/70 transition-transform group-hover:-translate-y-[1px] group-hover:translate-x-[1px]"
                aria-hidden
              />
            </a>
          ) : (
            <span className="line-clamp-2">{titleText}</span>
          )}
        </h3>

        {/* Snippet — sans, muted, tight. Highlight tag is <mark> from
         * OpenSearch; style it like a terminal selection. */}
        {hit.headline ? (
          <p
            className="mt-1 line-clamp-2 text-sm leading-relaxed text-muted-foreground [&_em]:rounded-sm [&_em]:bg-foreground/10 [&_em]:px-0.5 [&_em]:font-medium [&_em]:not-italic [&_em]:text-foreground [&_mark]:rounded-sm [&_mark]:bg-foreground/10 [&_mark]:px-0.5 [&_mark]:font-medium [&_mark]:text-foreground"
            dangerouslySetInnerHTML={{ __html: hit.headline }}
          />
        ) : hit.content ? (
          <p className="mt-1 line-clamp-2 text-sm leading-relaxed text-muted-foreground">
            {hit.content}
          </p>
        ) : null}

        <CardBody
          hit={hit}
          onOpenChat={onOpenChat}
          onDownload={onDownload}
        />

        {(hit.related_count ?? 0) > 0 && (
          <RelatedFooter
            docID={hit.id}
            count={hit.related_count ?? 0}
            onNavigate={onNavigateRelated}
          />
        )}
      </div>
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
