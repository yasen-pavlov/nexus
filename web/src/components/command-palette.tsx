import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Dialog as DialogPrimitive } from "@base-ui/react/dialog";
import { Search } from "lucide-react";

import {
  Dialog,
  DialogOverlay,
  DialogPortal,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

export type PaletteGroup = "Pages" | "Connectors" | "Actions";

export interface PaletteItem {
  id: string;
  group: PaletteGroup;
  label: string;
  /** muted secondary text shown beneath the label */
  hint?: string;
  /** pre-rendered 32px leading tile (caller controls hue) */
  icon: ReactNode;
  /** chip on the right (e.g. "g s", "↵") */
  keyboardHint?: string;
  onSelect: () => void;
}

export interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  items: PaletteItem[];
  /** Optional placeholder override */
  placeholder?: string;
}

// Item ordering in groups follows insertion order — the parent decides what
// matters most. The palette only re-orders when filtering ranks an exact
// label-prefix match above a substring match within the same group.
const GROUP_ORDER: PaletteGroup[] = ["Pages", "Connectors", "Actions"];

interface RankedItem extends PaletteItem {
  rank: number;
}

function rankItem(item: PaletteItem, q: string): number | null {
  if (q === "") return 0;
  const label = item.label.toLowerCase();
  const hint = (item.hint ?? "").toLowerCase();
  const group = item.group.toLowerCase();
  if (label.startsWith(q)) return 0;
  if (label.includes(q)) return 1;
  if (hint.includes(q)) return 2;
  if (group.includes(q)) return 3;
  return null;
}

function groupItems(items: PaletteItem[], query: string) {
  const q = query.trim().toLowerCase();
  const ranked: RankedItem[] = [];
  for (const it of items) {
    const r = rankItem(it, q);
    if (r === null) continue;
    ranked.push({ ...it, rank: r });
  }
  const byGroup = new Map<PaletteGroup, RankedItem[]>();
  for (const it of ranked) {
    const arr = byGroup.get(it.group) ?? [];
    arr.push(it);
    byGroup.set(it.group, arr);
  }
  for (const [, arr] of byGroup) {
    arr.sort((a, b) => a.rank - b.rank);
  }
  // Preserve canonical group order; drop empties.
  const ordered: { group: PaletteGroup; items: RankedItem[] }[] = [];
  for (const g of GROUP_ORDER) {
    const arr = byGroup.get(g);
    if (arr && arr.length > 0) ordered.push({ group: g, items: arr });
  }
  return ordered;
}

export function CommandPalette({
  open,
  onOpenChange,
  items,
  placeholder = "Jump to or search…",
}: Readonly<CommandPaletteProps>) {
  const [query, setQuery] = useState("");
  // userActiveId tracks the user's explicit selection (via arrows / hover).
  // Reset to null whenever the visible list changes by way of `query`.
  const [userActiveId, setUserActiveId] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Focus the input on open. base-ui's Dialog moves focus to the popup
  // root by default — defer one tick so our focus call wins.
  useEffect(() => {
    if (!open) return;
    const id = globalThis.setTimeout(() => inputRef.current?.focus(), 0);
    return () => globalThis.clearTimeout(id);
  }, [open]);

  const grouped = useMemo(() => groupItems(items, query), [items, query]);
  const flat = useMemo(() => grouped.flatMap((g) => g.items), [grouped]);

  // Derive the active row from user input + the current filtered list. If
  // the user's pick disappeared (filter narrowed) or never picked one,
  // fall back to the first row. This avoids a setState-in-effect dance.
  const activeId =
    userActiveId && flat.some((it) => it.id === userActiveId)
      ? userActiveId
      : (flat[0]?.id ?? null);

  // Auto-scroll the active row into view.
  useEffect(() => {
    if (!activeId || !listRef.current) return;
    const el = listRef.current.querySelector<HTMLElement>(
      `[data-palette-id="${CSS.escape(activeId)}"]`,
    );
    el?.scrollIntoView({ block: "nearest" });
  }, [activeId]);

  const moveActive = (delta: number) => {
    if (flat.length === 0) return;
    const idx = flat.findIndex((it) => it.id === activeId);
    const next = (idx + delta + flat.length) % flat.length;
    setUserActiveId(flat[next].id);
  };

  const handleOpenChange = (next: boolean) => {
    if (!next) {
      // Reset on close so the palette feels fresh next open. Safe to
      // setState here because we're in an event handler, not an effect.
      setQuery("");
      setUserActiveId(null);
    }
    onOpenChange(next);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      moveActive(1);
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      moveActive(-1);
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      const item = flat.find((it) => it.id === activeId);
      if (item) {
        item.onSelect();
        handleOpenChange(false);
      }
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Popup
          data-slot="dialog-content"
          aria-label="Command palette"
          className={cn(
            "fixed left-1/2 top-[15svh] z-50 flex w-[calc(100%-1.5rem)] max-w-[640px] -translate-x-1/2 flex-col overflow-hidden rounded-xl bg-popover text-popover-foreground ring-1 ring-foreground/10 outline-none",
            "shadow-[0_24px_64px_-24px_rgba(0,0,0,0.20),0_0_0_1px_color-mix(in_oklch,var(--primary)_8%,transparent)]",
            "data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-open:slide-in-from-top-4",
            "data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95 data-closed:slide-out-to-top-2",
          )}
        >
          <DialogPrimitive.Title className="sr-only">
            Command palette
          </DialogPrimitive.Title>

          {/* Hero input — borderless, large, marmalade caret. */}
          <div className="relative flex items-center border-b border-border/70">
            <Search
              aria-hidden
              className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
            />
            <input
              ref={inputRef}
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={placeholder}
              spellCheck={false}
              autoComplete="off"
              autoCorrect="off"
              className="h-14 w-full bg-transparent pl-11 pr-4 text-[15px] tracking-[-0.005em] text-foreground placeholder:text-muted-foreground/70 focus:outline-none"
              aria-label="Search palette"
              aria-controls="palette-list"
              aria-activedescendant={
                activeId ? `palette-item-${activeId}` : undefined
              }
            />
          </div>

          {/* Result list */}
          <div
            id="palette-list"
            ref={listRef}
            role="listbox"
            className="max-h-[min(420px,55svh)] overflow-y-auto px-1.5 py-1.5"
          >
            {flat.length === 0 ? (
              <EmptyResult query={query} />
            ) : (
              grouped.map((g) => (
                <div key={g.group} className="mb-1 last:mb-0">
                  <div className="px-2.5 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/70">
                    {g.group}
                  </div>
                  <div className="flex flex-col">
                    {g.items.map((it) => (
                      <PaletteRow
                        key={it.id}
                        item={it}
                        active={it.id === activeId}
                        onHover={() => setUserActiveId(it.id)}
                        onSelect={() => {
                          it.onSelect();
                          handleOpenChange(false);
                        }}
                      />
                    ))}
                  </div>
                </div>
              ))
            )}
          </div>

          {/* Footer hints */}
          <div className="flex items-center justify-between border-t border-border/70 bg-muted/40 px-3 py-2 text-[11px] text-muted-foreground">
            <div className="flex items-center gap-3">
              <KeyHint label="Navigate" keys={["↑", "↓"]} />
              <KeyHint label="Select" keys={["↵"]} />
              <KeyHint label="Close" keys={["Esc"]} />
            </div>
            <div className="hidden items-center gap-1 sm:flex">
              <span
                aria-hidden
                className="inline-block size-1.5 rounded-full bg-primary/70"
              />
              <span className="text-muted-foreground/80">
                {flat.length} {flat.length === 1 ? "result" : "results"}
              </span>
            </div>
          </div>
        </DialogPrimitive.Popup>
      </DialogPortal>
    </Dialog>
  );
}

function PaletteRow({
  item,
  active,
  onSelect,
  onHover,
}: Readonly<{
  item: PaletteItem;
  active: boolean;
  onSelect: () => void;
  onHover: () => void;
}>) {
  return (
    <button
      type="button"
      role="option"
      id={`palette-item-${item.id}`}
      data-palette-id={item.id}
      aria-selected={active}
      data-active={active || undefined}
      onClick={onSelect}
      onMouseMove={onHover}
      className={cn(
        "relative flex w-full items-center gap-3 rounded-md px-2.5 py-2 text-left text-[13.5px] transition-colors",
        "hover:bg-accent/40",
        "data-[active=true]:bg-primary/10 data-[active=true]:text-foreground",
        "data-[active=true]:before:absolute data-[active=true]:before:inset-y-1.5 data-[active=true]:before:left-0 data-[active=true]:before:w-[2px] data-[active=true]:before:rounded-full data-[active=true]:before:bg-primary",
      )}
    >
      <span className="shrink-0">{item.icon}</span>
      <span className="flex min-w-0 flex-1 flex-col leading-tight">
        <span className="truncate font-medium text-foreground">
          {item.label}
        </span>
        {item.hint && (
          <span className="truncate text-[12px] text-muted-foreground">
            {item.hint}
          </span>
        )}
      </span>
      {item.keyboardHint && (
        <span className="shrink-0 rounded-md border border-border/70 bg-background/60 px-1.5 py-0.5 font-mono text-[11px] tracking-[0.02em] text-muted-foreground">
          {item.keyboardHint}
        </span>
      )}
    </button>
  );
}

function EmptyResult({ query }: Readonly<{ query: string }>) {
  return (
    <div className="flex flex-col items-center gap-2 px-4 py-10 text-center">
      <div
        aria-hidden
        className="flex size-9 items-center justify-center rounded-md bg-muted text-muted-foreground"
      >
        <Search className="size-4" />
      </div>
      <div className="text-[13px] text-muted-foreground">
        Nothing matches{" "}
        {query ? (
          <span className="rounded bg-muted px-1.5 py-0.5 font-medium text-foreground">
            {query}
          </span>
        ) : (
          "your search"
        )}
      </div>
    </div>
  );
}

function KeyHint({ label, keys }: Readonly<{ label: string; keys: string[] }>) {
  return (
    <span className="inline-flex items-center gap-1">
      {keys.map((k) => (
        <kbd
          key={k}
          className="inline-flex h-4 min-w-4 items-center justify-center rounded border border-border/70 bg-background/60 px-1 font-mono text-[10px] leading-none text-foreground/80"
        >
          {k}
        </kbd>
      ))}
      <span className="text-muted-foreground/80">{label}</span>
    </span>
  );
}

/**
 * Helper to render the leading 32px tile for a palette item.
 * - "primary" → marmalade-tinted (matches empty-state medallion)
 * - "source" → uses the source hue via a CSS var
 *
 * Use this from the call site, not inside CommandPalette, so the consumer
 * keeps full control over what icon / hue lives in each row.
 */
export function PaletteIcon({
  children,
  tone = "primary",
  sourceVar,
}: Readonly<{
  children: ReactNode;
  tone?: "primary" | "source" | "muted";
  sourceVar?: string; // e.g. "--source-imap"
}>) {
  if (tone === "source" && sourceVar) {
    return (
      <span
        aria-hidden
        style={
          {
            "--chip-hue": `var(${sourceVar})`,
            backgroundColor:
              "color-mix(in oklch, var(--chip-hue) 14%, transparent)",
            color: "var(--chip-hue)",
          } as React.CSSProperties
        }
        className="flex size-8 items-center justify-center rounded-md ring-1 ring-[color:color-mix(in_oklch,var(--chip-hue)_18%,transparent)]"
      >
        {children}
      </span>
    );
  }
  if (tone === "muted") {
    return (
      <span
        aria-hidden
        className="flex size-8 items-center justify-center rounded-md bg-muted text-muted-foreground"
      >
        {children}
      </span>
    );
  }
  return (
    <span
      aria-hidden
      className="flex size-8 items-center justify-center rounded-md bg-primary/15 text-primary"
    >
      {children}
    </span>
  );
}
