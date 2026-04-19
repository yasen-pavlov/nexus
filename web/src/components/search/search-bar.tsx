import { useEffect, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { ArrowUp, Search, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { FOCUS_SEARCH_EVENT } from "@/hooks/use-global-shortcuts";
import type { SearchParams } from "@/lib/search-params";
import { cn } from "@/lib/utils";

// useNavigate without `from` can't narrow `search` to a route, so TS types
// it as `never`. Cast at the boundary; payload stays typed via SearchParams.
type AnyNavigate = (opts: { search: SearchParams; replace?: boolean }) => void;

interface Props {
  params: SearchParams;
}

const DEBOUNCE_MS = 300;

type Mode = "search" | "ask";

/**
 * Hero surface. Two modes — Search (URL-driven, debounced) and Ask (RAG,
 * not wired yet). The Ask affordance telegraphs the conversational future
 * without requiring it to ship today.
 */
export function SearchBar({ params }: Props) {
  const navigate = useNavigate() as unknown as AnyNavigate;
  const [value, setValue] = useState(params.q ?? "");
  const [mode, setMode] = useState<Mode>("search");
  const searchInputRef = useRef<HTMLInputElement>(null);

  // Mirror URL → input when the URL changes from *elsewhere* (e.g. a result
  // click navigates with a different query). Tracking the last q we synced
  // from lets us skip the mirror during the user's own typing round-trip
  // (type → debounce → navigate → params.q updates → value already matches),
  // and uses the "adjust state during rendering" pattern rather than a
  // useEffect(setValue) that the React Compiler rules flag.
  const externalQ = params.q ?? "";
  const [lastSyncedQ, setLastSyncedQ] = useState(externalQ);
  if (lastSyncedQ !== externalQ) {
    setLastSyncedQ(externalQ);
    setValue(externalQ);
  }

  // Listen for the global "/" shortcut. The handler dispatches a custom
  // event so we don't have to thread a ref through the route tree.
  useEffect(() => {
    const onFocus = () => {
      const el = searchInputRef.current;
      if (!el) return;
      el.focus();
      el.select();
    };
    window.addEventListener(FOCUS_SEARCH_EVENT, onFocus);
    return () => window.removeEventListener(FOCUS_SEARCH_EVENT, onFocus);
  }, []);

  // Debounced commit to URL (search mode only).
  useEffect(() => {
    if (mode !== "search") return;
    const next = value.trim();
    if (next === (params.q ?? "")) return;
    const t = window.setTimeout(() => {
      navigate({
        search: { ...params, q: next || undefined },
        replace: true,
      });
    }, DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [value, params, navigate, mode]);

  const submitAsk = () => {
    // Stub: pass 2+ wires this to the RAG endpoint. For now, commit to the
    // URL so the query survives a mode toggle.
    const next = value.trim();
    navigate({
      search: { ...params, q: next || undefined },
      replace: true,
    });
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (mode !== "ask") return;
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submitAsk();
    }
  };

  return (
    <div className="flex flex-col gap-2">
      <ModeToggle mode={mode} onChange={setMode} />

      {mode === "search" ? (
        <div className="relative">
          <Search
            aria-hidden
            className="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
          />
          <input
            ref={searchInputRef}
            type="search"
            placeholder="Search across everything you've indexed"
            className={cn(
              "h-12 w-full rounded-xl border border-input bg-card px-10 text-[17px] tracking-[-0.005em] text-foreground shadow-xs placeholder:text-muted-foreground/70",
              "transition-[border-color,box-shadow] outline-none",
              "focus-visible:border-ring focus-visible:ring-4 focus-visible:ring-ring/15",
            )}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            autoFocus
          />
        </div>
      ) : (
        <div
          className={cn(
            "relative flex items-end rounded-xl border border-input bg-card shadow-xs transition-[border-color,box-shadow]",
            "focus-within:border-ring focus-within:ring-4 focus-within:ring-ring/15",
          )}
        >
          <Sparkles
            aria-hidden
            className="pointer-events-none absolute left-3.5 top-3.5 size-4 text-primary/80"
          />
          <textarea
            placeholder="Ask anything across your email, chats, and files…"
            rows={2}
            className="min-h-[4.25rem] w-full resize-none bg-transparent py-3 pl-10 pr-14 text-[15px] leading-6 tracking-[-0.005em] outline-none placeholder:text-muted-foreground/70"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={handleKeyDown}
            autoFocus
          />
          <Button
            type="button"
            size="icon"
            onClick={submitAsk}
            disabled={!value.trim()}
            aria-label="Ask"
            className="absolute bottom-2 right-2 size-8 rounded-lg"
          >
            <ArrowUp className="size-4" aria-hidden />
          </Button>
        </div>
      )}

      {mode === "ask" && (
        <p className="px-1 text-[11px] text-muted-foreground/80">
          Answers with citations. Not available yet — your query will be
          handled as a regular search for now.
        </p>
      )}
    </div>
  );
}

function ModeToggle({
  mode,
  onChange,
}: {
  mode: Mode;
  onChange: (m: Mode) => void;
}) {
  return (
    <div className="inline-flex h-7 w-fit items-center rounded-full border border-border bg-card p-0.5 text-xs">
      <ModeButton
        label="Search"
        icon={Search}
        active={mode === "search"}
        onClick={() => onChange("search")}
      />
      <ModeButton
        label="Ask"
        icon={Sparkles}
        active={mode === "ask"}
        onClick={() => onChange("ask")}
      />
    </div>
  );
}

function ModeButton({
  label,
  icon: Icon,
  active,
  onClick,
}: {
  label: string;
  icon: typeof Search;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "inline-flex h-6 items-center gap-1.5 rounded-full px-2.5 font-medium transition-colors",
        active
          ? "bg-primary text-primary-foreground shadow-xs"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      <Icon className="size-3.5" aria-hidden />
      <span>{label}</span>
    </button>
  );
}
