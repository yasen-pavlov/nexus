import { useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { Input } from "@/components/ui/input";
import type { SearchParams } from "@/lib/search-params";

// useNavigate without `from` can't narrow `search` to a route, so TS types
// it as `never`. At runtime the navigate call works; this helper bypasses
// the strict check while keeping our payload type-safe via SearchParams.
type AnyNavigate = (opts: { search: SearchParams; replace?: boolean }) => void;

interface Props {
  params: SearchParams;
}

const DEBOUNCE_MS = 300;

/**
 * The URL is the source of truth for the query — this input is a mirror.
 * Typing updates local state immediately and schedules a debounced navigation
 * that commits the new query to the URL. The search hook reads `q` from the
 * URL, so reloading or sharing a link reproduces the exact results.
 */
export function SearchBar({ params }: Props) {
  const navigate = useNavigate() as unknown as AnyNavigate;
  const [value, setValue] = useState(params.q ?? "");

  // Keep the input in sync when the URL changes from elsewhere (back/forward,
  // external navigation, reset).
  useEffect(() => {
    setValue(params.q ?? "");
  }, [params.q]);

  // Debounce typed input → URL.
  useEffect(() => {
    const next = value.trim();
    if (next === (params.q ?? "")) return;
    const t = window.setTimeout(() => {
      navigate({
        search: { ...params, q: next || undefined },
        replace: true,
      });
    }, DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [value, params.q, navigate]);

  return (
    <div className="relative">
      <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        type="search"
        placeholder="Search across all your data…"
        className="h-10 pl-9"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        autoFocus
      />
    </div>
  );
}
