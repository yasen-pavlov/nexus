import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import type { Document, DocumentHit, RelatedEdge } from "@/lib/api-types";
import { useRelated } from "@/hooks/use-related";

// Human-readable labels per relation type + direction. Unknown types
// fall back to the raw string so new relation types degrade gracefully.
const RELATION_LABELS: Record<
  string,
  { outgoing: string; incoming: string }
> = {
  attachment_of: { outgoing: "Attachment of", incoming: "Attachments" },
  reply_to: { outgoing: "Reply to", incoming: "Replies" },
  member_of_thread: {
    outgoing: "Part of thread",
    incoming: "Thread messages",
  },
  member_of_window: {
    outgoing: "In conversation",
    incoming: "Messages in window",
  },
};

function labelFor(type: string, direction: "outgoing" | "incoming"): string {
  return RELATION_LABELS[type]?.[direction] ?? type;
}

function groupBy<T>(items: T[], key: (t: T) => string): Map<string, T[]> {
  const out = new Map<string, T[]>();
  for (const it of items) {
    const k = key(it);
    const bucket = out.get(k) ?? [];
    bucket.push(it);
    out.set(k, bucket);
  }
  return out;
}

interface Props {
  docID: string;
  count: number;
  onNavigate: (doc: DocumentHit) => void;
}

/**
 * Collapsed: a dim mono line `▸ related (n)` — no border, no block,
 * reads as another metadata line alongside the source handle. Expanded:
 * the richer grouped tree (sections for "Points to" / "Referenced by",
 * incoming grouped by relation type) for scannability.
 */
export function RelatedFooter({ docID, count, onNavigate }: Props) {
  const [expanded, setExpanded] = useState(false);
  const { data, isLoading, error } = useRelated(docID, expanded);

  return (
    <div className="mt-2">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="inline-flex items-baseline gap-1 font-mono text-[11px] text-muted-foreground/80 transition-colors hover:text-foreground"
      >
        {expanded ? (
          <ChevronDown className="size-3 shrink-0 self-center" aria-hidden />
        ) : (
          <ChevronRight className="size-3 shrink-0 self-center" aria-hidden />
        )}
        <span>related</span>
        <span className="tabular-nums text-muted-foreground/60">({count})</span>
      </button>

      {expanded && (
        <div className="mt-2 space-y-3 pl-4 text-xs">
          {isLoading && (
            <div className="font-mono text-[11px] text-muted-foreground/60">
              loading…
            </div>
          )}
          {error && (
            <div className="font-mono text-[11px] text-destructive">
              {error instanceof Error ? error.message : "failed to load"}
            </div>
          )}

          {data && data.outgoing.length > 0 && (
            <Section title="Points to">
              {data.outgoing.map((edge, i) => (
                <EdgeRow
                  key={`o-${i}`}
                  edge={edge}
                  prefix={`${labelFor(edge.relation.type, "outgoing")}:`}
                  onNavigate={onNavigate}
                />
              ))}
            </Section>
          )}

          {data && data.incoming.length > 0 && (
            <Section title="Referenced by">
              {[...groupBy(data.incoming, (e) => e.relation.type).entries()].map(
                ([type, bucket]) => (
                  <div key={type}>
                    <div className="font-medium text-muted-foreground">
                      {labelFor(type, "incoming")}
                      <span className="ml-1 tabular-nums text-muted-foreground/60">
                        ({bucket.length})
                      </span>
                    </div>
                    <ul className="mt-0.5 space-y-0.5 pl-3">
                      {bucket.map((edge, i) => (
                        <EdgeRow
                          key={`i-${type}-${i}`}
                          edge={edge}
                          onNavigate={onNavigate}
                        />
                      ))}
                    </ul>
                  </div>
                ),
              )}
            </Section>
          )}
        </div>
      )}
    </div>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground/70">
        {title}
      </div>
      <div className="mt-1 space-y-1">{children}</div>
    </div>
  );
}

function EdgeRow({
  edge,
  prefix,
  onNavigate,
}: {
  edge: RelatedEdge;
  prefix?: string;
  onNavigate: (doc: DocumentHit) => void;
}) {
  const fallbackID =
    edge.relation.target_source_id ?? edge.relation.target_id ?? "?";

  return (
    <li className="flex min-w-0 items-baseline gap-1">
      {prefix && (
        <span className="shrink-0 text-muted-foreground">{prefix}</span>
      )}
      {edge.document ? (
        <button
          type="button"
          onClick={() => onNavigate(toHit(edge.document!))}
          className="truncate text-left text-foreground/90 transition-colors hover:text-foreground hover:underline"
        >
          {edge.document.title || edge.document.source_id}
        </button>
      ) : (
        <span
          className="truncate font-mono text-muted-foreground/60"
          title={fallbackID}
        >
          {fallbackID}
        </span>
      )}
    </li>
  );
}

function toHit(doc: Document): DocumentHit {
  return { ...doc, rank: 0 };
}
