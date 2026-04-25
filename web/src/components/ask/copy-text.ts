// Plaintext serialiser for the assistant's "Copy" button. Inserts
// `[N]` markers at each citation's span_end and appends a sources
// legend keyed by 1-based evidence index. Pure; safe to call from any
// render or event handler.

import type { ChatMessage, ChunkPreview } from "@/lib/api-types";

export function buildCopyText(
  text: string,
  citations: ChatMessage["citations"],
  evidence: ChunkPreview[],
): string {
  const numberByDocID = new Map<string, number>();
  evidence.forEach((c, i) => numberByDocID.set(c.id, i + 1));

  const sorted = (citations ?? [])
    .filter((c) => c.span_end >= 0 && c.span_end <= text.length)
    .slice()
    .sort((a, b) => a.span_end - b.span_end);

  // Walk back-to-front so insertions don't shift earlier offsets.
  let body = text;
  for (let i = sorted.length - 1; i >= 0; i--) {
    const c = sorted[i];
    const num = numberByDocID.get(c.doc_id);
    if (!num) continue;
    body = body.slice(0, c.span_end) + ` [${num}]` + body.slice(c.span_end);
  }

  if (evidence.length === 0) return body;
  const legend = evidence
    .map((c, i) => `[${i + 1}] ${c.title}${c.date ? ` — ${c.date}` : ""}`)
    .join("\n");
  return `${body}\n\nSources:\n${legend}`;
}
