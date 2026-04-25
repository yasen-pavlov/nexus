import {
  Children,
  Fragment,
  cloneElement,
  isValidElement,
  useMemo,
  type ReactElement,
  type ReactNode,
} from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { TooltipProvider } from "@/components/ui/tooltip";
import type { ChatCitation, ChunkPreview } from "@/lib/api-types";
import { cn } from "@/lib/utils";

import { CitationPill } from "./citation-pill";

export interface AnswerStreamProps {
  text: string;
  citations: ChatCitation[];
  evidence: ChunkPreview[];
  isStreaming: boolean;
  onJumpToEvidence: (docID: string) => void;
}

const SENTINEL_OPEN = "§§CITE";
const SENTINEL_CLOSE = "§§";

interface PreparedAnswer {
  /** Text with `§§CITE:idx§§` tokens inserted at each resolvable citation's
   *  span_end. Indexes refer to positions in `citations`. */
  body: string;
  /** Citations in the same order as their sentinel index. */
  citations: ChatCitation[];
}

/**
 * Build the markdown body with citation sentinels woven in. Walking the
 * citations back-to-front lets us splice sentinels without shifting the
 * later ones' offsets.
 */
function prepareAnswer(
  text: string,
  citations: ChatCitation[],
  hasEvidence: (docID: string) => boolean,
): PreparedAnswer {
  const resolvable = citations
    .filter((c) => hasEvidence(c.doc_id))
    .filter((c) => c.span_start >= 0 && c.span_end >= c.span_start)
    .filter((c) => c.span_end <= text.length);

  // Stable order: span_end ascending, then span_start.
  const ordered = resolvable
    .slice()
    .sort((a, b) => a.span_end - b.span_end || a.span_start - b.span_start);

  let body = text;
  // Build the sentinel-token list in the SAME order as ordered citations
  // so the index in `citations` below matches the `idx` we splice in.
  for (let i = ordered.length - 1; i >= 0; i--) {
    const c = ordered[i];
    body = body.slice(0, c.span_end) + `${SENTINEL_OPEN}:${i}${SENTINEL_CLOSE}` + body.slice(c.span_end);
  }
  return { body, citations: ordered };
}

/**
 * Replace sentinels in a single string with segments of text + pills.
 * Returns an array of ReactNodes ready to be flattened into a parent's
 * children prop.
 */
function splitSentinels(
  raw: string,
  citations: ChatCitation[],
  evidenceByDocID: Map<string, ChunkPreview>,
  numberByDocID: Map<string, number>,
  onJump: (docID: string) => void,
): ReactNode[] {
  if (!raw.includes(SENTINEL_OPEN)) return [raw];

  const out: ReactNode[] = [];
  const pattern = /§§CITE:(\d+)§§/g;
  let cursor = 0;
  let m: RegExpExecArray | null;
  while ((m = pattern.exec(raw)) !== null) {
    if (m.index > cursor) {
      out.push(raw.slice(cursor, m.index));
    }
    const idx = Number(m[1]);
    const c = citations[idx];
    if (c) {
      const evidenceItem = evidenceByDocID.get(c.doc_id);
      const num = numberByDocID.get(c.doc_id) ?? -1;
      out.push(
        <CitationPill
          key={`p-${c.doc_id}-${c.span_start}-${c.span_end}`}
          number={num}
          sourceType={evidenceItem?.source ?? ""}
          title={evidenceItem?.title ?? c.doc_id}
          citedText={c.cited_text}
          onClick={() => onJump(c.doc_id)}
        />,
      );
    }
    cursor = m.index + m[0].length;
  }
  if (cursor < raw.length) {
    out.push(raw.slice(cursor));
  }
  return out;
}

/**
 * Recursively walk a children tree and replace sentinel-bearing text
 * with text + CitationPill segments. react-markdown v9 doesn't expose
 * a `text` component override, so we hook every block/inline renderer
 * via this walker.
 */
function withPills(
  children: ReactNode,
  citations: ChatCitation[],
  evidenceByDocID: Map<string, ChunkPreview>,
  numberByDocID: Map<string, number>,
  onJump: (docID: string) => void,
): ReactNode {
  return Children.map(children, (child) => {
    if (typeof child === "string") {
      return splitSentinels(
        child,
        citations,
        evidenceByDocID,
        numberByDocID,
        onJump,
      ).map((p, i) => <Fragment key={i}>{p}</Fragment>);
    }
    if (isValidElement(child)) {
      const el = child as ReactElement<{ children?: ReactNode }>;
      if (el.props?.children !== undefined) {
        return cloneElement(el, {
          children: withPills(
            el.props.children,
            citations,
            evidenceByDocID,
            numberByDocID,
            onJump,
          ),
        });
      }
    }
    return child;
  });
}

/**
 * Streamed answer prose rendered as markdown. Citations resolve into
 * inline tonal pills — span_end positions get sentinel tokens before
 * markdown parsing, then a custom text renderer swaps them for
 * <CitationPill>. After the body, a small "Sources" rail offers a
 * numeric mini-toc so readers can jump even when a chunk wasn't cited
 * inline.
 */
export function AnswerStream({
  text,
  citations,
  evidence,
  isStreaming,
  onJumpToEvidence,
}: Readonly<AnswerStreamProps>) {
  const numberByDocID = useMemo(() => {
    const map = new Map<string, number>();
    evidence.forEach((c, i) => map.set(c.id, i + 1));
    return map;
  }, [evidence]);

  const evidenceByDocID = useMemo(() => {
    const map = new Map<string, ChunkPreview>();
    for (const c of evidence) map.set(c.id, c);
    return map;
  }, [evidence]);

  const prepared = useMemo(
    () => prepareAnswer(text, citations, (id) => evidenceByDocID.has(id)),
    [text, citations, evidenceByDocID],
  );

  // Closure that any renderer can use to splice pills into its
  // children. Memoised on the inputs so cloneElement isn't allocating
  // on every keystroke during streaming.
  const pillify = (children: ReactNode): ReactNode =>
    withPills(
      children,
      prepared.citations,
      evidenceByDocID,
      numberByDocID,
      onJumpToEvidence,
    );

  return (
    <TooltipProvider delay={250}>
      <div
        role="article"
        aria-label="Answer"
        aria-live="off"
        className={cn(
          "answer-prose text-[15px] leading-[26px] tracking-[-0.003em] text-foreground",
        )}
      >
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          components={{
            p: ({ children }) => (
              <p className="mb-4 last:mb-0">{pillify(children)}</p>
            ),
            ul: ({ children }) => (
              <ul className="my-4 ml-5 list-disc space-y-1.5 marker:text-muted-foreground/60">
                {pillify(children)}
              </ul>
            ),
            ol: ({ children }) => (
              <ol className="my-4 ml-5 list-decimal space-y-1.5 marker:text-muted-foreground/60">
                {pillify(children)}
              </ol>
            ),
            li: ({ children }) => (
              <li className="leading-[24px]">{pillify(children)}</li>
            ),
            strong: ({ children }) => (
              <strong className="font-semibold text-foreground">
                {pillify(children)}
              </strong>
            ),
            em: ({ children }) => <em className="italic">{pillify(children)}</em>,
            h1: ({ children }) => (
              <h1 className="mb-2 mt-5 text-[18px] font-semibold tracking-tight text-foreground first:mt-0">
                {pillify(children)}
              </h1>
            ),
            h2: ({ children }) => (
              <h2 className="mb-2 mt-5 text-[16px] font-semibold tracking-tight text-foreground first:mt-0">
                {pillify(children)}
              </h2>
            ),
            h3: ({ children }) => (
              <h3 className="mb-2 mt-4 text-[15px] font-semibold tracking-tight text-foreground first:mt-0">
                {pillify(children)}
              </h3>
            ),
            blockquote: ({ children }) => (
              <blockquote className="my-4 border-l-2 border-primary/40 pl-3 text-muted-foreground">
                {pillify(children)}
              </blockquote>
            ),
            code: ({ children, ...props }) => {
              const isBlock = "data-language" in props;
              // Inline code rarely contains citations; preserve the
              // raw children to keep code formatting honest.
              if (isBlock) {
                return (
                  <code className="block rounded-md bg-muted px-3 py-2 font-mono text-[13px] leading-[20px]">
                    {children}
                  </code>
                );
              }
              return (
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[13px]">
                  {children}
                </code>
              );
            },
            pre: ({ children }) => (
              <pre className="my-4 overflow-x-auto rounded-md bg-muted p-3 font-mono text-[13px] leading-[20px]">
                {children}
              </pre>
            ),
            a: ({ href, children }) => (
              <a
                href={href}
                target="_blank"
                rel="noreferrer"
                className="text-primary underline decoration-primary/40 underline-offset-2 hover:decoration-primary"
              >
                {pillify(children)}
              </a>
            ),
          }}
        >
          {prepared.body}
        </ReactMarkdown>
        {isStreaming && (
          <span
            aria-hidden
            className="ml-[2px] inline-block h-[1em] w-[1ch] translate-y-[2px] animate-pulse bg-foreground/70"
          />
        )}
      </div>

      {evidence.length > 0 && (
        <div className={cn("mt-5 flex flex-col gap-1.5")}>
          <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
            Sources
          </div>
          <div className="flex flex-wrap gap-1.5">
            {evidence.map((c, i) => (
              <CitationPill
                key={`source-${c.id}`}
                number={i + 1}
                sourceType={c.source}
                title={c.title}
                onClick={() => onJumpToEvidence(c.id)}
              />
            ))}
          </div>
        </div>
      )}
    </TooltipProvider>
  );
}
