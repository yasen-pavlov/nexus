import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Slider } from "@/components/ui/slider";

import {
  useRerankSettings,
  type UseRerankSettings,
} from "@/hooks/use-embedding-settings";
import type { RerankSettings } from "@/lib/api-types";
import {
  DEFAULT_RERANK_MODEL,
  RERANK_MODELS,
  RERANK_PROVIDERS,
  type ModelOption,
  type RerankProvider,
} from "@/lib/model-catalog";
import { cn } from "@/lib/utils";

function providerLabel(value: RerankProvider): string {
  return RERANK_PROVIDERS.find((p) => p.value === value)?.label ?? value;
}

export function RerankForm() {
  const ctx = useRerankSettings();

  if (ctx.isPending) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-9 w-full max-w-xl" />
      </div>
    );
  }

  // Re-seed form state whenever the backend hands us a new snapshot. Keyed
  // remount keeps the useState initializer authoritative and avoids the
  // useEffect(setForm(data)) + savedRef-during-render pattern that the
  // React Compiler rules reject.
  return (
    <RerankFormInner key={rerankFingerprint(ctx.data ?? null)} ctx={ctx} />
  );
}

function rerankFingerprint(s: RerankSettings | null): string {
  if (!s) return "empty";
  return `${s.provider}|${s.model}|${s.api_key}|${s.min_score}`;
}

function RerankFormInner({ ctx }: { ctx: UseRerankSettings }) {
  const { data, update } = ctx;
  const saved: RerankSettings = data ?? {
    provider: "",
    model: "",
    api_key: "",
    min_score: 0.4,
  };

  const [form, setForm] = useState<RerankSettings>(saved);
  const [replacingKey, setReplacingKey] = useState(false);

  const dirtyProvider = form.provider !== saved.provider;
  const dirtyModel = form.model !== saved.model;
  const dirtyKey =
    replacingKey && form.api_key !== "" && !form.api_key.startsWith("****");
  const dirtyMinScore = Math.abs(form.min_score - saved.min_score) > 1e-6;
  const dirty = dirtyProvider || dirtyModel || dirtyKey || dirtyMinScore;

  const needsAPIKey = ["voyage", "cohere"].includes(form.provider);

  const handleProviderChange = (next: RerankProvider) => {
    // Returning to the saved provider — restore the saved draft so the
    // masked key display reappears on cycle-back.
    const returningToSaved = saved.provider === next;
    setForm((f) => ({
      ...f,
      provider: next,
      model: returningToSaved
        ? saved.model
        : (DEFAULT_RERANK_MODEL[next] ?? ""),
      api_key: returningToSaved ? saved.api_key : "",
    }));
    setReplacingKey(false);
  };

  const revert = () => {
    setForm(saved);
    setReplacingKey(false);
  };

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        update.mutate(form);
      }}
    >
      <div className="grid max-w-xl gap-5">
        <Field
          label="Provider"
          hint={
            form.provider === ""
              ? "No reranking — search results use their retrieval scores directly."
              : "The cross-encoder that rescores the top candidates."
          }
        >
          <Select
            value={form.provider}
            onValueChange={(v) => handleProviderChange(v as RerankProvider)}
          >
            <SelectTrigger className="h-10 w-full">
              <SelectValue placeholder="Pick a provider" />
            </SelectTrigger>
            <SelectContent>
              {RERANK_PROVIDERS.map((p) => (
                <SelectItem key={p.value || "none"} value={p.value}>
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>

        {form.provider !== "" && (
          <Field
            label="Model"
            hint="Pick a curated option or type your own."
          >
            <ModelCombobox
              key={form.provider}
              value={form.model}
              onChange={(v) => setForm((f) => ({ ...f, model: v }))}
              options={RERANK_MODELS[form.provider] ?? []}
            />
          </Field>
        )}

        {form.provider !== "" && (
          <Field
            label="Score floor"
            hint={`Docs with a rerank score below ${form.min_score.toFixed(2)} are dropped. Tighten it if noise leaks into results; loosen if good results are getting filtered.`}
          >
            <div className="flex items-center gap-3">
              <Slider
                value={Math.round(form.min_score * 100)}
                onValueChange={(v) =>
                  setForm((f) => ({ ...f, min_score: v / 100 }))
                }
                min={0}
                max={100}
                step={1}
                aria-label="Rerank score floor"
                className="max-w-sm"
              />
              <span className="min-w-[3rem] text-right font-mono text-[13px] tabular-nums text-muted-foreground">
                {form.min_score.toFixed(2)}
              </span>
            </div>
          </Field>
        )}

        {needsAPIKey && (
          <Field
            label="API key"
            hint={`Paste your ${providerLabel(form.provider)} API key. Stored encrypted; only the last four characters show after saving. Leave blank to reuse the embedding key when the provider matches.`}
          >
            {form.api_key &&
            form.api_key.startsWith("****") &&
            !replacingKey ? (
              <div className="flex items-center gap-2">
                <div className="flex h-10 min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-background px-3 font-mono text-[13px] text-muted-foreground">
                  <span aria-hidden className="select-none tracking-[0.3em]">
                    ••••
                  </span>
                  <span className="truncate">{form.api_key.slice(4)}</span>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setReplacingKey(true);
                    setForm((f) => ({ ...f, api_key: "" }));
                  }}
                >
                  Replace
                </Button>
              </div>
            ) : (
              <div className="flex items-center gap-2">
                <Input
                  type="password"
                  value={form.api_key}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, api_key: e.target.value }))
                  }
                  placeholder="paste your key or leave blank"
                  className="h-10 flex-1 font-mono text-[13px]"
                  autoFocus={replacingKey}
                />
                {replacingKey && saved.api_key && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setReplacingKey(false);
                      setForm((f) => ({
                        ...f,
                        api_key: saved.api_key,
                      }));
                    }}
                  >
                    Cancel
                  </Button>
                )}
              </div>
            )}
          </Field>
        )}
      </div>

      {dirty && (
        <div className="sticky bottom-0 -mx-6 -mb-6 mt-4 flex items-center justify-between gap-3 border-t border-border/70 bg-card/95 px-6 py-3 backdrop-blur">
          <div className="flex items-center gap-2 text-[12px] text-muted-foreground">
            <span
              aria-hidden
              className="size-1.5 shrink-0 rounded-full bg-primary"
            />
            <span>Draft · not saved yet</span>
          </div>
          <div className="flex gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={revert}
              disabled={update.isPending}
            >
              Revert
            </Button>
            <Button type="submit" size="sm" disabled={update.isPending}>
              {update.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        </div>
      )}
    </form>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label className="text-[13px] font-medium">{label}</Label>
      {children}
      {hint && (
        <p className="text-[12px] leading-[1.5] text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  );
}

/**
 * Local copy of the combobox — rerank has a smaller catalog (no dimension
 * chips, fewer notes) but the interaction is identical. Inlining avoids a
 * generic primitive pulling in EmbeddingProvider typing.
 *
 * Parents should `key={provider}` (or similar) this component so external
 * value changes reset its internal edit buffer via remount rather than a
 * useEffect(setQuery(value)) that the React Compiler rules flag.
 */
function ModelCombobox({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (v: string) => void;
  options: ModelOption[];
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState(value);
  const [highlighted, setHighlighted] = useState(0);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    // Pristine state — input still holds the saved value because the user
    // hasn't typed anything yet. Show every option so opening the dropdown
    // is a discovery surface, not a one-row self-match.
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
          placeholder="e.g. rerank-v3.5"
          className="h-10 pr-10 font-mono text-[13px]"
          role="combobox"
          aria-expanded={open}
          aria-autocomplete="list"
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
                {opt.notes && (
                  <span className="text-[11.5px] text-muted-foreground max-w-[180px] truncate">
                    {opt.notes}
                  </span>
                )}
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
