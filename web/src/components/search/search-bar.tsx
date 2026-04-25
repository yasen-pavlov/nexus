import { useEffect, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Search } from "lucide-react";
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

/**
 * Hero search input. Conversational RAG lives on its own /ask route
 * (sidebar entry + `g k` chord), so this surface is single-purpose:
 * URL-debounced search across the index.
 */
export function SearchBar({ params }: Readonly<Props>) {
  const navigate = useNavigate() as unknown as AnyNavigate;
  const [value, setValue] = useState(params.q ?? "");
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
    globalThis.addEventListener(FOCUS_SEARCH_EVENT, onFocus);
    return () => globalThis.removeEventListener(FOCUS_SEARCH_EVENT, onFocus);
  }, []);

  // Debounced commit to URL.
  useEffect(() => {
    const next = value.trim();
    if (next === (params.q ?? "")) return;
    const t = globalThis.setTimeout(() => {
      navigate({
        search: { ...params, q: next || undefined },
        replace: true,
      });
    }, DEBOUNCE_MS);
    return () => globalThis.clearTimeout(t);
  }, [value, params, navigate]);

  return (
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
  );
}
