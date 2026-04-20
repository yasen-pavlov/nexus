import { Link } from "@tanstack/react-router";
import type { ComponentType, SVGProps } from "react";
import {
  ArrowRight,
  Database,
  SearchX,
  Sparkles,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type IconComponent = ComponentType<SVGProps<SVGSVGElement>>;

interface EmptyStateProps {
  icon: IconComponent;
  title: string;
  description?: React.ReactNode;
  children?: React.ReactNode;
}

/**
 * Shared shell for all three empty states. Medallion icon at top, headline,
 * optional description, optional action row. Center-aligned, max-w-md.
 */
function EmptyState({
  icon: Icon,
  title,
  description,
  children,
}: EmptyStateProps) {
  return (
    <div className="mx-auto flex max-w-md flex-col items-center px-4 py-14 text-center">
      <div className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary">
        <Icon className="size-5" aria-hidden strokeWidth={2} />
      </div>
      <h2 className="mt-4 text-[17px] font-semibold tracking-[-0.01em] text-foreground">
        {title}
      </h2>
      {description && (
        <p className="mt-1.5 text-[13.5px] leading-relaxed text-muted-foreground">
          {description}
        </p>
      )}
      {children && <div className="mt-5 w-full">{children}</div>}
    </div>
  );
}

export function NoConnectorsState() {
  return (
    <EmptyState
      icon={Database}
      title="Nothing indexed yet"
      description="Connectors pull content into Nexus — a folder, an email account, a Paperless instance, a Telegram account. Add one and search lights up when the first sync completes."
    >
      <div className="flex justify-center">
        <Button render={<Link to="/connectors" />}>
          Add a connector
          <ArrowRight className="ml-1 size-4" aria-hidden />
        </Button>
      </div>
    </EmptyState>
  );
}

const EXAMPLE_QUERIES = [
  "tax return 2025",
  "invoice from",
  "meeting notes",
  "vacation photos",
];

interface WelcomeStateProps {
  /** When provided, example query chips become clickable and fire this
   *  callback with the clicked query string. Without it, chips render
   *  as visually inert hints. */
  onPickExample?: (q: string) => void;
}

export function WelcomeState({ onPickExample }: WelcomeStateProps) {
  const interactive = typeof onPickExample === "function";
  return (
    <EmptyState
      icon={Sparkles}
      title="Search across everything"
      description="Your email, chats, documents, and files — indexed and connected. Start typing above, or try one of these:"
    >
      <div className="flex flex-wrap justify-center gap-1.5">
        {EXAMPLE_QUERIES.map((q) => (
          <button
            key={q}
            type="button"
            onClick={interactive ? () => onPickExample?.(q) : undefined}
            disabled={!interactive}
            className={cn(
              "h-7 rounded-full border border-border bg-card px-3 text-[12.5px] text-foreground/90 transition-colors",
              interactive
                ? "cursor-pointer hover:border-primary/40 hover:bg-primary/5 hover:text-foreground"
                : "cursor-default",
            )}
          >
            {q}
          </button>
        ))}
      </div>
    </EmptyState>
  );
}

export function NoResultsState({ query }: { query: string }) {
  return (
    <EmptyState
      icon={SearchX}
      title="No matches"
      description={
        <>
          Nothing matched{" "}
          <span className="rounded bg-muted px-1.5 py-0.5 font-medium text-foreground">
            {query}
          </span>
        </>
      }
    >
      <ul className="mx-auto max-w-xs space-y-1.5 text-left text-[13px] text-muted-foreground">
        <li className="flex items-start gap-2">
          <span
            aria-hidden
            className="mt-[7px] size-1 shrink-0 rounded-full bg-muted-foreground/50"
          />
          <span>Clear date filters if any are set</span>
        </li>
        <li className="flex items-start gap-2">
          <span
            aria-hidden
            className="mt-[7px] size-1 shrink-0 rounded-full bg-muted-foreground/50"
          />
          <span>Remove source filters and try again</span>
        </li>
        <li className="flex items-start gap-2">
          <span
            aria-hidden
            className="mt-[7px] size-1 shrink-0 rounded-full bg-muted-foreground/50"
          />
          <span>Try fewer or more general terms</span>
        </li>
      </ul>
    </EmptyState>
  );
}
