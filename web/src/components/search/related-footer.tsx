import type { Document, DocumentHit, RelatedEdge } from "@/lib/api-types";
import { useRelated } from "@/hooks/use-related";
import { SourceChip } from "@/components/source-chip";

// Direction-aware labels per relation type. Unknown types fall back to the
// raw string so new relation types degrade gracefully.
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
  onNavigate: (doc: DocumentHit) => void;
}

/**
 * Expanded related content. Mounted inside a bordered region at the bottom
 * of the ResultCard when the user clicks "N related"; the parent controls
 * open/close, so this component renders its body immediately without an
 * internal toggle.
 */
export function RelatedFooter({ docID, onNavigate }: Readonly<Props>) {
  const { data, isLoading, error } = useRelated(docID, true);

  if (isLoading) {
    return (
      <div className="text-[12.5px] text-muted-foreground">
        Loading related…
      </div>
    );
  }

  if (error) {
    return (
      <div className="text-[12.5px] text-destructive">
        {error instanceof Error ? error.message : "Failed to load related"}
      </div>
    );
  }

  if (!data) return null;

  const hasOutgoing = data.outgoing.length > 0;
  const hasIncoming = data.incoming.length > 0;

  if (!hasOutgoing && !hasIncoming) {
    return (
      <div className="text-[12.5px] text-muted-foreground">
        No related documents.
      </div>
    );
  }

  const groupedIncoming = groupBy(data.incoming, (e) => e.relation.type);

  return (
    <div className="flex flex-col gap-3 text-[13px]">
      {hasOutgoing && (
        <Section title="Points to">
          <ul className="flex flex-col gap-1">
            {data.outgoing.map((edge) => (
              <OutgoingRow
                key={`o-${edge.relation.type}-${edge.relation.target_id ?? edge.relation.target_source_id ?? ""}`}
                edge={edge}
                onNavigate={onNavigate}
              />
            ))}
          </ul>
        </Section>
      )}

      {hasIncoming && (
        <Section title="Referenced by">
          <div className="flex flex-col gap-2.5">
            {[...groupedIncoming.entries()].map(([type, bucket]) => (
              <IncomingGroup
                key={type}
                type={type}
                edges={bucket}
                onNavigate={onNavigate}
              />
            ))}
          </div>
        </Section>
      )}
    </div>
  );
}

function Section({
  title,
  children,
}: Readonly<{
  title: string;
  children: React.ReactNode;
}>) {
  return (
    <div>
      <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
        {title}
      </div>
      {children}
    </div>
  );
}

function OutgoingRow({
  edge,
  onNavigate,
}: Readonly<{
  edge: RelatedEdge;
  onNavigate: (d: DocumentHit) => void;
}>) {
  const label = labelFor(edge.relation.type, "outgoing");
  const fallbackID =
    edge.relation.target_source_id ?? edge.relation.target_id ?? "?";
  return (
    <li className="flex min-w-0 items-center gap-2">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      {edge.document ? (
        <>
          <SourceChip type={edge.document.source_type} variant="compact" />
          <button
            type="button"
            onClick={() => onNavigate(toHit(edge.document!))}
            className="truncate text-left text-foreground/90 transition-colors hover:text-foreground hover:underline"
          >
            {edge.document.title || edge.document.source_id}
          </button>
        </>
      ) : (
        <span
          className="truncate text-muted-foreground/70"
          title={fallbackID}
        >
          {fallbackID}
        </span>
      )}
    </li>
  );
}

function IncomingGroup({
  type,
  edges,
  onNavigate,
}: Readonly<{
  type: string;
  edges: RelatedEdge[];
  onNavigate: (d: DocumentHit) => void;
}>) {
  return (
    <div>
      <div className="flex items-baseline gap-1.5 text-[12.5px]">
        <span className="font-medium text-foreground">
          {labelFor(type, "incoming")}
        </span>
        <span className="tabular-nums text-muted-foreground/70">
          ({edges.length})
        </span>
      </div>
      <ul className="mt-1 flex flex-col gap-1 border-l border-border pl-2.5">
        {edges.map((edge, i) => (
          <IncomingRow
            key={`${type}-${i}`}
            edge={edge}
            onNavigate={onNavigate}
          />
        ))}
      </ul>
    </div>
  );
}

function IncomingRow({
  edge,
  onNavigate,
}: Readonly<{
  edge: RelatedEdge;
  onNavigate: (d: DocumentHit) => void;
}>) {
  const fallbackID =
    edge.relation.target_source_id ?? edge.relation.target_id ?? "?";
  return (
    <li className="flex min-w-0 items-center gap-2">
      {edge.document ? (
        <>
          <SourceChip type={edge.document.source_type} variant="compact" />
          <button
            type="button"
            onClick={() => onNavigate(toHit(edge.document!))}
            className="truncate text-left text-foreground/90 transition-colors hover:text-foreground hover:underline"
          >
            {edge.document.title || edge.document.source_id}
          </button>
        </>
      ) : (
        <>
          <span aria-hidden className="size-5 shrink-0" />
          <span
            className="truncate text-muted-foreground/70"
            title={fallbackID}
          >
            {fallbackID}
          </span>
        </>
      )}
    </li>
  );
}

function toHit(doc: Document): DocumentHit {
  return { ...doc, rank: 0 };
}
