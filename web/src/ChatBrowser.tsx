import { useEffect, useState, useCallback, useRef } from 'react';
import { getConversationMessages, type Document, type Relation } from './api';

interface Props {
  sourceType: string;
  conversationID: string;
  anchorMessageID?: number;
  onClose: () => void;
}

// messageIDOf pulls the canonical numeric message ID out of a chunk's
// metadata. Telegram stores it as `message_id`; other chat connectors
// follow the same convention so the UI can stay source-agnostic.
function messageIDOf(doc: Document): number | undefined {
  const raw = doc.metadata?.message_id;
  return typeof raw === 'number' ? raw : undefined;
}

// replyToTargetSourceID returns the source_id a message replies to, or
// null if the message isn't a reply. We walk Relations[] rather than the
// raw reply_to metadata so this works for any connector emitting the
// typed relation (Telegram today, WhatsApp later).
function replyToTargetSourceID(doc: Document): string | null {
  const rel = (doc.relations || []).find((r: Relation) => r.type === 'reply_to');
  return rel?.target_source_id ?? null;
}

// ChatBrowser paginates a chat conversation in chronological order. Pulls
// from /api/conversations/{source_type}/{conversation_id}/messages with
// cursor-based infinite scroll in both directions.
export default function ChatBrowser({ sourceType, conversationID, anchorMessageID, onClose }: Props) {
  const [messages, setMessages] = useState<Document[]>([]);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [loadingNewer, setLoadingNewer] = useState(false);
  const [cursorBefore, setCursorBefore] = useState<string | undefined>(undefined);
  const [cursorAfter, setCursorAfter] = useState<string | undefined>(undefined);
  const [hasOlder, setHasOlder] = useState(true);
  const [hasNewer, setHasNewer] = useState(true);
  const [error, setError] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);

  // Build a source_id → doc index so reply quotes can render the quoted
  // text inline without a secondary fetch when the replied-to message
  // is already loaded in the same page.
  const bySourceID = new Map<string, Document>();
  for (const m of messages) bySourceID.set(m.source_id, m);

  // Initial load — pull the newest page (no `before` cursor). Later we
  // can expose "jump to anchor" that seeds the range around the anchor
  // message; for PoC we just dump the tail of the conversation.
  useEffect(() => {
    let cancelled = false;
    setError('');
    setMessages([]);
    (async () => {
      try {
        const res = await getConversationMessages(sourceType, conversationID, { limit: 50 });
        if (cancelled) return;
        setMessages(res.messages ?? []);
        setCursorBefore(res.next_before);
        setHasOlder(!!res.next_before);
        setHasNewer(false); // nothing newer than the latest page
        setCursorAfter(undefined);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load chat');
      }
    })();
    return () => { cancelled = true; };
  }, [sourceType, conversationID]);

  const loadOlder = useCallback(async () => {
    if (!cursorBefore || loadingOlder) return;
    setLoadingOlder(true);
    try {
      const res = await getConversationMessages(sourceType, conversationID, { before: cursorBefore, limit: 50 });
      const older = res.messages ?? [];
      setMessages((prev) => [...older, ...prev]);
      setCursorBefore(res.next_before);
      setHasOlder(!!res.next_before);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load older messages');
    } finally {
      setLoadingOlder(false);
    }
  }, [cursorBefore, sourceType, conversationID, loadingOlder]);

  const loadNewer = useCallback(async () => {
    if (!cursorAfter || loadingNewer) return;
    setLoadingNewer(true);
    try {
      const res = await getConversationMessages(sourceType, conversationID, { after: cursorAfter, limit: 50 });
      const newer = res.messages ?? [];
      setMessages((prev) => [...prev, ...newer]);
      setCursorAfter(res.next_after);
      setHasNewer(!!res.next_after);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load newer messages');
    } finally {
      setLoadingNewer(false);
    }
  }, [cursorAfter, sourceType, conversationID, loadingNewer]);

  // Scroll to the anchor message once it's rendered. Pragma: first match
  // wins; if the anchor isn't in the current page the UI just shows the
  // newest page and the user can scroll back.
  useEffect(() => {
    if (!anchorMessageID || messages.length === 0) return;
    const el = document.getElementById(`msg-${anchorMessageID}`);
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }, [anchorMessageID, messages.length]);

  // Infinite scroll trigger — when the user scrolls near either edge,
  // kick off pagination. IntersectionObserver would be cleaner but for a
  // PoC the scroll-handler approach reads as less code.
  const onScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (el.scrollTop < 80 && hasOlder && !loadingOlder) void loadOlder();
    if (el.scrollTop + el.clientHeight > el.scrollHeight - 80 && hasNewer && !loadingNewer) void loadNewer();
  };

  return (
    <div className="chat-browser">
      <div className="chat-header">
        <button className="chat-close" onClick={onClose}>← Back</button>
        <div className="chat-title">
          <div className="chat-title-main">{sourceType} chat</div>
          <div className="chat-title-sub">{conversationID}</div>
        </div>
      </div>
      {error && <div className="error">{error}</div>}
      <div className="chat-scroll" ref={scrollRef} onScroll={onScroll}>
        {loadingOlder && <div className="chat-loading">Loading older…</div>}
        {!hasOlder && messages.length > 0 && <div className="chat-boundary">Beginning of conversation</div>}
        {messages.map((m) => {
          const msgID = messageIDOf(m);
          const replyTarget = replyToTargetSourceID(m);
          const quoted = replyTarget ? bySourceID.get(replyTarget) : null;
          const isAnchor = msgID !== undefined && msgID === anchorMessageID;
          return (
            <div
              key={m.source_id}
              id={msgID !== undefined ? `msg-${msgID}` : undefined}
              className={`chat-message${isAnchor ? ' chat-message-anchor' : ''}`}
            >
              {quoted && (
                <div className="chat-reply-quote">
                  ↪ {(quoted.content || '').slice(0, 140)}
                </div>
              )}
              <div className="chat-message-meta">
                {new Date(m.created_at).toLocaleString()}
                {m.metadata?.sender_id ? <span className="chat-sender"> · {String(m.metadata.sender_id)}</span> : null}
              </div>
              <div className="chat-message-body">
                {m.content || <span className="chat-message-empty">(no text)</span>}
              </div>
            </div>
          );
        })}
        {loadingNewer && <div className="chat-loading">Loading newer…</div>}
        {messages.length === 0 && !loadingOlder && !loadingNewer && !error && (
          <div className="chat-empty">No messages in this conversation.</div>
        )}
      </div>
    </div>
  );
}
