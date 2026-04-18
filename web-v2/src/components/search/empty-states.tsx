import { Link } from "@tanstack/react-router";
import { ArrowRight, Database, SearchX } from "lucide-react";
import { Button } from "@/components/ui/button";

/**
 * Empty states share a quiet editorial grammar: left-aligned, one small
 * inline icon at most, and a single monospace accent per state. The
 * border-l rule is the connective signature across all three.
 */
function EmptyFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="mx-auto max-w-xl px-2 py-12">
      <div className="border-l-2 border-border/60 pl-5">{children}</div>
    </div>
  );
}

export function NoConnectorsState() {
  return (
    <EmptyFrame>
      <div className="flex items-center gap-2 text-muted-foreground">
        <Database className="size-3.5" aria-hidden />
        <span className="font-mono text-xs tracking-tight">no_connectors</span>
      </div>

      <h2 className="mt-3 text-lg font-medium leading-tight">
        Nothing indexed yet.
      </h2>

      <p className="mt-1.5 text-sm leading-relaxed text-muted-foreground">
        Point Nexus at your first data source — a folder, an email account, a
        Paperless instance, a Telegram account — and search lights up as soon
        as the initial sync completes.
      </p>

      <Button size="sm" className="mt-4" render={<Link to="/connectors" />}>
        Add a connector
        <ArrowRight className="ml-1 size-3.5" aria-hidden />
      </Button>
    </EmptyFrame>
  );
}

const EXAMPLE_QUERIES = [
  "tax return",
  '"monthly report"',
  "vacation photos 2025",
  "from:alice invoice",
];

export function WelcomeState() {
  return (
    <EmptyFrame>
      <div className="font-mono text-xs tracking-tight text-muted-foreground">
        ready.
      </div>

      <h2 className="mt-3 text-lg font-medium leading-tight">
        Search across everything you&rsquo;ve indexed.
      </h2>

      <p className="mt-1.5 text-sm leading-relaxed text-muted-foreground">
        Type a word, a phrase, a filename. Works across mail, chats, documents
        and files at once.
      </p>

      <ul className="mt-4 space-y-1 font-mono text-xs text-muted-foreground">
        {EXAMPLE_QUERIES.map((q) => (
          <li key={q} className="flex items-baseline gap-2">
            <span className="text-muted-foreground/50">&gt;</span>
            <span>{q}</span>
          </li>
        ))}
      </ul>
    </EmptyFrame>
  );
}

export function NoResultsState({ query }: { query: string }) {
  return (
    <EmptyFrame>
      <div className="flex items-center gap-2 text-muted-foreground">
        <SearchX className="size-3.5" aria-hidden />
        <span className="font-mono text-xs tracking-tight">0 results</span>
      </div>

      <h2 className="mt-3 text-lg font-medium leading-tight">
        Nothing matched{" "}
        <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.9em] font-normal">
          {query}
        </span>
        .
      </h2>

      <ul className="mt-4 space-y-1.5 text-sm text-muted-foreground">
        <li className="flex items-baseline gap-2">
          <span className="text-muted-foreground/60">&mdash;</span>
          <span>Clear date filters if you&rsquo;ve set them</span>
        </li>
        <li className="flex items-baseline gap-2">
          <span className="text-muted-foreground/60">&mdash;</span>
          <span>Remove source filters and try again</span>
        </li>
        <li className="flex items-baseline gap-2">
          <span className="text-muted-foreground/60">&mdash;</span>
          <span>Try fewer or more general terms</span>
        </li>
      </ul>
    </EmptyFrame>
  );
}
