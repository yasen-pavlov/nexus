import type { DocumentHit } from "@/lib/api-types";
import { cn } from "@/lib/utils";

interface Props {
  hit: DocumentHit;
  /** Tailwind line-clamp count. Defaults to 2. */
  lineClamp?: 1 | 2 | 3 | 4;
  className?: string;
}

// SnippetBody renders the search-hit snippet for a document, preferring the
// server-provided `headline` (which includes <em>/<mark> highlight tags) and
// falling back to raw content. Highlight spans get the marmalade-tinted
// accent treatment shared with the card chassis.
export function SnippetBody({
  hit,
  lineClamp = 2,
  className,
}: Readonly<Props>) {
  const clampClass =
    lineClamp === 1
      ? "line-clamp-1"
      : lineClamp === 2
        ? "line-clamp-2"
        : lineClamp === 3
          ? "line-clamp-3"
          : "line-clamp-4";

  if (hit.headline) {
    return (
      <p
        className={cn(
          clampClass,
          "text-[13.5px] leading-[1.55] text-muted-foreground",
          "[&_em]:rounded-sm [&_em]:bg-primary/15 [&_em]:px-0.5 [&_em]:font-medium [&_em]:not-italic [&_em]:text-foreground",
          "[&_mark]:rounded-sm [&_mark]:bg-primary/15 [&_mark]:px-0.5 [&_mark]:font-medium [&_mark]:text-foreground",
          className,
        )}
        dangerouslySetInnerHTML={{ __html: hit.headline }}
      />
    );
  }

  if (hit.content) {
    return (
      <p
        className={cn(
          clampClass,
          "text-[13.5px] leading-[1.55] text-muted-foreground",
          className,
        )}
      >
        {hit.content}
      </p>
    );
  }

  return null;
}
