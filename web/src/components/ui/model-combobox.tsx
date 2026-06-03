import { ChevronDown } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { Input } from "@/components/ui/input";
import type { ModelOption } from "@/lib/model-catalog";
import { cn } from "@/lib/utils";

export interface ModelComboboxProps {
  value: string;
  onChange: (value: string) => void;
  options: ModelOption[];
  placeholder?: string;
  /** Optionally show a "{N}d" dimension chip on each row. */
  showDimensions?: boolean;
}

/**
 * Combobox over a curated model list with custom-value fallback. Reused by
 * the embedding, rerank, and LLM admin forms; parents should `key={provider}`
 * the component so external `value` resets (provider switch in the outer
 * form) remount the combobox instead of needing a useEffect(setQuery(value))
 * sync.
 *
 * Pristine-state behavior: when the input still holds the saved value (the
 * user hasn't typed anything yet), opening the dropdown shows every option
 * — a discovery surface, not a one-row self-match.
 */
export function ModelCombobox({
  value,
  onChange,
  options,
  placeholder,
  showDimensions = true,
}: Readonly<ModelComboboxProps>) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState(value);
  const [highlighted, setHighlighted] = useState(0);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q || q === value.trim().toLowerCase()) return options;
    return options.filter(
      (o) =>
        o.value.toLowerCase().includes(q) ||
        o.label.toLowerCase().includes(q) ||
        (o.notes?.toLowerCase().includes(q) ?? false),
    );
  }, [options, query, value]);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (
        wrapperRef.current &&
        !wrapperRef.current.contains(e.target as Node)
      ) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const commit = (v: string) => {
    onChange(v);
    setQuery(v);
    setOpen(false);
    setHighlighted(0);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      setHighlighted((h) =>
        Math.min(h + 1, Math.max(filtered.length - 1, 0)),
      );
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlighted((h) => Math.max(h - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (open && filtered[highlighted]) commit(filtered[highlighted].value);
      else commit(query.trim());
    } else if (e.key === "Escape") {
      setOpen(false);
    }
  };

  return (
    <div ref={wrapperRef} className="relative">
      <div className="relative">
        <Input
          ref={inputRef}
          value={query}
          onChange={(e) => {
            const v = e.target.value;
            setQuery(v);
            onChange(v);
            setOpen(true);
            setHighlighted(0);
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
          placeholder={placeholder}
          className="h-10 pr-10 font-mono text-[13px]"
          role="combobox"
          aria-expanded={open}
          aria-autocomplete="list"
          aria-controls="model-listbox"
        />
        <button
          type="button"
          onClick={() => {
            setOpen((o) => !o);
            inputRef.current?.focus();
          }}
          className="absolute right-1.5 top-1/2 flex size-7 -translate-y-1/2 items-center justify-center rounded text-muted-foreground/70 transition-colors hover:text-foreground"
          aria-label="Toggle options"
          tabIndex={-1}
        >
          <ChevronDown
            className={cn("size-3.5 transition-transform", open && "rotate-180")}
            aria-hidden
          />
        </button>
      </div>

      {open && (filtered.length > 0 || query.trim()) && (
        <div className="absolute left-0 right-0 top-full z-20 mt-1 overflow-hidden rounded-lg border border-border bg-popover">
          <ul
            id="model-listbox"
            role="listbox"
            className="max-h-64 overflow-y-auto py-1"
          >
            {filtered.map((opt, i) => (
              <li
                key={opt.value}
                role="option"
                aria-selected={i === highlighted}
                data-highlighted={i === highlighted}
                onMouseEnter={() => setHighlighted(i)}
                onMouseDown={(e) => {
                  e.preventDefault();
                  commit(opt.value);
                }}
                className={cn(
                  "flex cursor-pointer items-center justify-between gap-3 px-3 py-2 text-[13px] transition-colors",
                  "data-[highlighted=true]:bg-accent data-[highlighted=true]:text-accent-foreground",
                )}
              >
                <span className="font-mono tabular-nums">{opt.label}</span>
                <span className="flex items-center gap-2 text-[11.5px] text-muted-foreground">
                  {showDimensions && opt.dimension && (
                    <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium tabular-nums text-muted-foreground/90">
                      {opt.dimension}d
                    </span>
                  )}
                  {opt.notes && (
                    <span className="max-w-[180px] truncate">{opt.notes}</span>
                  )}
                </span>
              </li>
            ))}
            {filtered.length === 0 && query.trim() && (
              <li
                role="option"
                aria-selected={true}
                onMouseDown={(e) => {
                  e.preventDefault();
                  commit(query.trim());
                }}
                className="flex cursor-pointer items-center justify-between gap-3 px-3 py-2 text-[13px] hover:bg-accent"
              >
                <span className="text-muted-foreground">Use custom model</span>
                <span className="font-mono text-[12px] text-foreground">
                  {query.trim()}
                </span>
              </li>
            )}
          </ul>
        </div>
      )}
    </div>
  );
}
