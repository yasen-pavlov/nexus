// Pure rendering helper for the streaming answer + citation pills.
//
// Citations stream alongside text deltas; the BE emits them with
// post-strip char offsets `[span_start, span_end]` into the assistant's
// running text. While a stream is in flight a citation may arrive whose
// `span_end` is past the current `text.length` — we hold it until a
// later delta extends the text past `span_end`. The pill anchors at
// `span_end` (Perplexity-style trailing pill); the cited substring is
// rendered as plain text and the pill sits immediately after it.
//
// The renderer that consumes Segment[] keys pills by
// `${doc_id}:${span_start}:${span_end}` so React doesn't reuse pill
// nodes when citations stream out of order.

import type { ChatCitation } from "./api-types";

export type Segment =
  | { kind: "text"; text: string }
  | { kind: "pill"; citation: ChatCitation };

/**
 * Walk the answer text + the citations we've received so far, producing
 * a flat segment list ready to render. Pure; safe to call on every
 * render.
 *
 * - Citations are sorted by `span_start` so pills appear in document
 *   order even if the stream delivered them out of sequence.
 * - Citations whose `span_end > text.length` are *deferred* — they're
 *   not rendered yet, so the pill never anchors into a position the
 *   answer hasn't streamed past. The next call (after another text
 *   delta extends the prefix) picks them up.
 * - Citations with malformed bounds (`span_start < 0`, `span_end <
 *   span_start`, `span_start > text.length`) are dropped silently — the
 *   BE shouldn't emit these but defensive parsing avoids a hard crash.
 * - Adjacent / overlapping pill spans are allowed; we anchor each at
 *   its own `span_end` and the in-between text segment is whatever
 *   slice sits between consecutive `span_end`s. If two citations share
 *   the same `span_end`, both pills render in arrival order.
 */
export function segmentAnswer(
  text: string,
  citations: ChatCitation[],
): Segment[] {
  if (text === "") return [];
  if (citations.length === 0) {
    return [{ kind: "text", text }];
  }

  const sorted = citations
    .filter((c) => isCitationDeliverable(c, text.length))
    .sort((a, b) => a.span_end - b.span_end || a.span_start - b.span_start);

  if (sorted.length === 0) {
    return [{ kind: "text", text }];
  }

  const segments: Segment[] = [];
  let cursor = 0;
  for (const c of sorted) {
    // The text leading up to and including the cited substring becomes
    // one text segment; the pill anchors immediately after it.
    if (c.span_end > cursor) {
      segments.push({ kind: "text", text: text.slice(cursor, c.span_end) });
      cursor = c.span_end;
    }
    segments.push({ kind: "pill", citation: c });
  }
  if (cursor < text.length) {
    segments.push({ kind: "text", text: text.slice(cursor) });
  }
  return segments;
}

function isCitationDeliverable(c: ChatCitation, textLen: number): boolean {
  if (c.span_start < 0 || c.span_end < c.span_start) return false;
  if (c.span_start > textLen) return false;
  // Held-back: span extends past current text — the next text delta
  // will move the cursor and a later segmentAnswer() call will pick it
  // up.
  if (c.span_end > textLen) return false;
  return true;
}
