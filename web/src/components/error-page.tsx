import { useState } from "react";
import { Link } from "@tanstack/react-router";
import {
  Compass,
  Home,
  OctagonAlert,
  RefreshCw,
  Search,
  ChevronDown,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export type ErrorKind = "404" | "error";

export interface ErrorPageProps {
  kind: ErrorKind;
  /** required when kind === "error"; ignored otherwise */
  error?: Error;
  /** when omitted, "Reload page" calls window.location.reload() */
  onReload?: () => void;
}

const COPY: Record<
  ErrorKind,
  { eyebrow: string; title: string; body: string }
> = {
  "404": {
    eyebrow: "404 · Off the map",
    title: "We couldn't find that page",
    body: "The link may have changed, or the resource was removed. Try searching from the home page.",
  },
  error: {
    eyebrow: "Something went wrong",
    title: "This page hit a snag",
    body: "An unexpected error broke this view. Going back home is your best move; the technical details are below if you need them.",
  },
};

/**
 * Shared 404 + error fallback. Designed as a pair so they feel like
 * sibling states: same medallion + eyebrow rhythm, accent shifts,
 * copy diverges. Lives inside the main scroll area — does NOT fill
 * the viewport itself.
 */
export function ErrorPage({ kind, error, onReload }: ErrorPageProps) {
  const copy = COPY[kind];
  const Icon = kind === "404" ? Compass : OctagonAlert;
  const accentVar = kind === "error" ? "--destructive" : "--primary";

  const handleReload = () => {
    if (onReload) onReload();
    else globalThis.location.reload();
  };

  return (
    <div className="mx-auto flex w-full max-w-[520px] flex-col items-center px-6 py-16 text-center">
      {/* Medallion */}
      <div
        aria-hidden
        style={
          {
            "--accent-hue": `var(${accentVar})`,
            backgroundColor:
              "color-mix(in oklch, var(--accent-hue) 12%, transparent)",
            color: "var(--accent-hue)",
            boxShadow:
              "inset 0 0 0 1px color-mix(in oklch, var(--accent-hue) 18%, transparent)",
          } as React.CSSProperties
        }
        className={cn(
          "relative flex size-[72px] items-center justify-center rounded-2xl",
          // subtle radial wash for depth
          "before:pointer-events-none before:absolute before:inset-0 before:rounded-2xl before:opacity-40 before:[background:radial-gradient(120%_120%_at_30%_15%,color-mix(in_oklch,var(--accent-hue)_20%,transparent)_0%,transparent_60%)]",
        )}
      >
        <Icon className="relative size-7" strokeWidth={1.75} />
      </div>

      {/* Eyebrow */}
      <div className="mt-5 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/80">
        {copy.eyebrow}
      </div>

      {/* Title */}
      <h1 className="mt-2 text-[24px] font-medium leading-[1.2] tracking-[-0.01em] text-foreground">
        {copy.title}
      </h1>

      {/* Body */}
      <p className="mt-3 text-[14px] leading-[1.6] text-muted-foreground">
        {copy.body}
      </p>

      {/* Actions */}
      <div className="mt-6 flex flex-wrap justify-center gap-2">
        <Button render={<Link to="/" />} className="gap-1.5">
          <Home className="size-3.5" aria-hidden />
          Go home
        </Button>
        {kind === "error" ? (
          <Button
            type="button"
            variant="ghost"
            onClick={handleReload}
            className="gap-1.5"
          >
            <RefreshCw className="size-3.5" aria-hidden />
            Reload page
          </Button>
        ) : (
          <Button
            render={<Link to="/" />}
            variant="ghost"
            className="gap-1.5"
          >
            <Search className="size-3.5" aria-hidden />
            Try search
          </Button>
        )}
      </div>

      {/* Disclosure: technical details (error variant only) */}
      {kind === "error" && error && <TechnicalDetails error={error} />}
    </div>
  );
}

function TechnicalDetails({ error }: { error: Error }) {
  const [open, setOpen] = useState(false);
  const stack = error.stack ?? error.message ?? String(error);

  return (
    <div className="mt-8 w-full max-w-md">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className={cn(
          "group flex w-full items-center justify-center gap-1.5 rounded-md px-2 py-1.5 text-[12px] text-muted-foreground transition-colors",
          "hover:text-foreground",
        )}
      >
        <ChevronDown
          className={cn(
            "size-3 transition-transform",
            open && "rotate-180",
          )}
          aria-hidden
        />
        Technical details
      </button>
      {open && (
        <div className="mt-2 max-h-40 overflow-auto rounded-md border border-border/70 bg-muted/40 p-3 text-left">
          <pre className="whitespace-pre-wrap break-words font-mono text-[11.5px] leading-[1.55] text-muted-foreground">
            {stack}
          </pre>
        </div>
      )}
    </div>
  );
}
