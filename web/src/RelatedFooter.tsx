import { useState, useCallback } from 'react';
import { getRelatedDocuments, type RelatedResponse, type RelatedEdge, type Document } from './api';

// Human-readable labels for the relation types the backend emits. Falls
// back to the raw type string for anything unknown so newly added
// relation types degrade gracefully until we ship UI polish.
const RELATION_LABELS: Record<string, { outgoing: string; incoming: string }> = {
  attachment_of: { outgoing: 'Attachment of', incoming: 'Attachments' },
  reply_to: { outgoing: 'Reply to', incoming: 'Replies' },
  member_of_thread: { outgoing: 'Part of thread', incoming: 'Thread messages' },
  member_of_window: { outgoing: 'In conversation', incoming: 'Contains messages' },
};

function labelFor(type: string, direction: 'outgoing' | 'incoming'): string {
  return RELATION_LABELS[type]?.[direction] ?? type;
}

// groupBy bins edges by relation type for readable display. A single
// "Attachments: 3" summary row beats rendering three separate edges
// when the target type is the same.
function groupBy<T>(items: T[], key: (t: T) => string): Map<string, T[]> {
  const out = new Map<T, T[]>() as unknown as Map<string, T[]>;
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
  onNavigate?: (doc: Document) => void;
}

// RelatedFooter lazily fetches /api/documents/{id}/related when the user
// expands the section. Collapsed by default so the common case (skim
// search results) doesn't fan out a request per result.
export default function RelatedFooter({ docID, onNavigate }: Props) {
  const [expanded, setExpanded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState<RelatedResponse | null>(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      setData(await getRelatedDocuments(docID));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load relations');
    } finally {
      setLoading(false);
    }
  }, [docID]);

  const toggle = () => {
    const next = !expanded;
    setExpanded(next);
    if (next && !data && !loading) void load();
  };

  const renderEdge = (edge: RelatedEdge, key: string) => (
    <li key={key} className="related-edge">
      {edge.document ? (
        <button type="button" className="related-link" onClick={() => onNavigate?.(edge.document!)}>
          {edge.document.title || edge.document.source_id}
        </button>
      ) : (
        <span className="related-missing">
          (not indexed)
          <span className="related-hint"> · {edge.relation.target_source_id}</span>
        </span>
      )}
    </li>
  );

  return (
    <div className="related-footer">
      <button type="button" className="related-toggle" onClick={toggle}>
        {expanded ? '▼' : '▶'} Related
      </button>
      {expanded && (
        <div className="related-body">
          {loading && <div className="related-loading">Loading…</div>}
          {error && <div className="related-error">{error}</div>}
          {data && !loading && (
            <>
              {data.outgoing.length === 0 && data.incoming.length === 0 && (
                <div className="related-empty">No related documents.</div>
              )}
              {data.outgoing.length > 0 && (
                <div className="related-section">
                  <div className="related-section-title">Points to</div>
                  <ul>
                    {data.outgoing.map((edge, i) => (
                      <li key={`out-${i}`} className="related-edge">
                        <span className="related-type">{labelFor(edge.relation.type, 'outgoing')}:</span>{' '}
                        {edge.document ? (
                          <button type="button" className="related-link" onClick={() => onNavigate?.(edge.document!)}>
                            {edge.document.title || edge.document.source_id}
                          </button>
                        ) : (
                          <span className="related-missing">(not indexed)</span>
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {data.incoming.length > 0 && (
                <div className="related-section">
                  <div className="related-section-title">Referenced by</div>
                  {[...groupBy(data.incoming, (e) => e.relation.type).entries()].map(([type, edges]) => (
                    <div key={type} className="related-group">
                      <div className="related-type">
                        {labelFor(type, 'incoming')} ({edges.length})
                      </div>
                      <ul>{edges.map((edge, i) => renderEdge(edge, `in-${type}-${i}`))}</ul>
                    </div>
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}
