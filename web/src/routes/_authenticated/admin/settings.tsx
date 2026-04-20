import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Archive,
  BellRing,
  Brain,
  ChevronDown,
  Database,
  HardDriveDownload,
  Scale,
  Sliders,
  Sparkles,
  Wrench,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";

import { SettingsSection } from "@/components/admin/settings-section";
import { CacheSection } from "@/components/admin/cache-section";
import { MaintenanceSection } from "@/components/admin/maintenance-section";
import { RankingForm } from "@/components/admin/ranking-form";
import { RerankForm } from "@/components/admin/rerank-form";
import { RetentionForm } from "@/components/admin/retention-form";

import {
  useEmbeddingSettings,
  type UseEmbeddingSettings,
} from "@/hooks/use-embedding-settings";
import { useSystemStats } from "@/hooks/use-system-stats";

import type { EmbeddingSettings } from "@/lib/api-types";
import {
  DEFAULT_EMBEDDING_MODEL,
  EMBEDDING_MODELS,
  EMBEDDING_PROVIDERS,
  type EmbeddingProvider,
  type ModelOption,
} from "@/lib/model-catalog";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_authenticated/admin/settings")({
  component: SettingsPage,
});

// Human-readable provider label for copy surfaces. Falls back to the raw
// value so a future provider we forgot to map still reads sensibly.
function providerLabel(value: EmbeddingProvider): string {
  return EMBEDDING_PROVIDERS.find((p) => p.value === value)?.label ?? value;
}

// Provider-specific API key placeholder for the replace-key input.
function apiKeyPlaceholder(provider: EmbeddingProvider): string {
  if (provider === "openai") return "sk-...";
  if (provider === "voyage") return "pa-...";
  return "paste your key";
}

// ---------------------------------------------------------------------------
// Shell
// ---------------------------------------------------------------------------

interface NavItem {
  id: string;
  label: string;
  icon: typeof Brain;
  ordinal: string;
}

const NAV: NavItem[] = [
  { id: "embeddings", label: "Embeddings", icon: Brain, ordinal: "01" },
  { id: "rerank", label: "Reranking", icon: Scale, ordinal: "02" },
  { id: "ranking", label: "Search ranking", icon: Sliders, ordinal: "03" },
  { id: "retention", label: "History retention", icon: Archive, ordinal: "04" },
  { id: "cache", label: "Binary cache", icon: HardDriveDownload, ordinal: "05" },
  { id: "maintenance", label: "Maintenance", icon: Wrench, ordinal: "06" },
];

// Active-section scoring, for use by the scroll listener below.
//
// The nav's "active row" is the section the user is currently reading. That
// almost always means "the section whose header most recently passed the
// sticky offset line near the top of the viewport" — call that the
// most-recently-passed rule. It breaks down in one spot: when the scroll
// container has reached its max and the last section's header never got a
// chance to cross the offset line. In that case we fall back to picking the
// bottom-most section whose header is visible inside the scroller, so the
// nav still surfaces the section the user actually landed on.
export function scoreActiveSection(
  positions: { id: string; top: number }[],
  viewportHeight: number,
  atBottom: boolean,
  offset: number,
): string | null {
  if (positions.length === 0) return null;

  if (atBottom) {
    const visible = positions.filter(
      (p) => p.top >= 0 && p.top < viewportHeight,
    );
    if (visible.length > 0) {
      visible.sort((a, b) => b.top - a.top);
      return visible[0].id;
    }
  }

  const passed = positions.filter((p) => p.top <= offset);
  if (passed.length > 0) {
    passed.sort((a, b) => b.top - a.top);
    return passed[0].id;
  }
  return positions[0].id;
}

const NAV_OFFSET_PX = 80;

function SettingsPage() {
  const [active, setActive] = useState<string>(NAV[0].id);
  // While a click-driven scroll animation is in-flight the scroll listener
  // would otherwise flip the active row to whichever section the viewport
  // passes through. Suppress updates until the scroll settles.
  const suppressUntilRef = useRef(0);

  useEffect(() => {
    const sections = NAV.map((n) => document.getElementById(n.id)).filter(
      (el): el is HTMLElement => !!el,
    );
    if (sections.length === 0) return;

    // Walk up from a section to the nearest scrolling ancestor — AppShell's
    // <main> in practice. Falls back to window for standalone renders.
    const scroller: HTMLElement | null = (() => {
      let el: HTMLElement | null = sections[0].parentElement;
      while (el) {
        const overflow = getComputedStyle(el).overflowY;
        if (overflow === "auto" || overflow === "scroll") return el;
        el = el.parentElement;
      }
      return null;
    })();

    let frame = 0;
    const update = () => {
      frame = 0;
      if (performance.now() < suppressUntilRef.current) return;

      const scrollerTop = scroller
        ? scroller.getBoundingClientRect().top
        : 0;
      const viewportHeight = scroller ? scroller.clientHeight : window.innerHeight;
      const atBottom = scroller
        ? scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 1
        : window.scrollY + window.innerHeight >=
          document.documentElement.scrollHeight - 1;

      const positions = sections.map((el) => ({
        id: el.id,
        top: el.getBoundingClientRect().top - scrollerTop,
      }));

      const next = scoreActiveSection(
        positions,
        viewportHeight,
        atBottom,
        NAV_OFFSET_PX,
      );
      if (next) setActive(next);
    };

    const onScroll = () => {
      if (frame) return;
      frame = requestAnimationFrame(update);
    };

    const target: EventTarget = scroller ?? globalThis;
    target.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll, { passive: true });
    update();

    return () => {
      target.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, []);

  const jumpTo = (id: string) => {
    const el = document.getElementById(id);
    if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
    setActive(id);
    // Smooth scroll can take ~600ms; hold the clicked row authoritative
    // through the animation so the user's choice sticks.
    suppressUntilRef.current = performance.now() + 900;
  };

  return (
    <div className="mx-auto w-full max-w-6xl flex-1 px-6 py-8">
      <header className="mb-8">
        <h1 className="text-[20px] font-medium tracking-[-0.005em] text-foreground">
          Settings
        </h1>
        <p className="mt-1 text-[13.5px] leading-[1.55] text-muted-foreground">
          Tune the engines that power search, manage the data Nexus keeps, and
          reach for the bigger levers when something isn&apos;t right.
        </p>
      </header>

      <MobileSectionsBar
        active={active}
        onJump={(id) => jumpTo(id)}
      />

      <div className="grid gap-10 md:grid-cols-[220px_minmax(0,1fr)]">
        <aside className="hidden md:block">
          <nav
            aria-label="Settings sections"
            className="sticky top-6 space-y-0.5 border-l border-border/60 pl-1"
          >
            <div className="mb-2 pl-3 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/70">
              Sections
            </div>
            {NAV.map((n) => {
              const Icon = n.icon;
              const isActive = active === n.id;
              return (
                <button
                  key={n.id}
                  type="button"
                  data-active={isActive}
                  onClick={() => jumpTo(n.id)}
                  className={cn(
                    "group relative flex w-full items-center gap-2.5 rounded-md py-1.5 pl-3 pr-2 text-left text-[13px] transition-colors",
                    "text-muted-foreground hover:text-foreground",
                    "data-[active=true]:bg-primary/10 data-[active=true]:text-foreground",
                    "data-[active=true]:before:absolute data-[active=true]:before:left-[-1px] data-[active=true]:before:top-1.5 data-[active=true]:before:bottom-1.5 data-[active=true]:before:w-[2px] data-[active=true]:before:rounded-full data-[active=true]:before:bg-primary",
                  )}
                >
                  <span
                    className={cn(
                      "font-mono text-[10px] tabular-nums transition-colors",
                      isActive
                        ? "text-primary/80"
                        : "text-muted-foreground/45 group-hover:text-muted-foreground/80",
                    )}
                  >
                    {n.ordinal}
                  </span>
                  <Icon className="size-3.5 shrink-0" aria-hidden strokeWidth={2.25} />
                  <span className="truncate">{n.label}</span>
                </button>
              );
            })}
          </nav>
        </aside>

        <div className="flex min-w-0 flex-col gap-10">
          <SettingsSection
            id="embeddings"
            label="Engine · 01"
            title="Embeddings"
            icon={Brain}
            description="Semantic search reads documents through this model. Changing the provider or model re-indexes everything — it's the slowest setting on the page."
          >
            <EmbeddingsForm />
          </SettingsSection>

          <SettingsSection
            id="rerank"
            label="Engine · 02"
            title="Reranking"
            icon={Scale}
            description="A second-pass cross-encoder that sharpens the top results. Optional but worth it for multilingual queries."
          >
            <RerankForm />
          </SettingsSection>

          <SettingsSection
            id="ranking"
            label="Signals · 03"
            title="Search ranking"
            icon={Sliders}
            description="Recency decay and per-source trust weights. Changes apply on the next query — no re-index."
          >
            <RankingForm />
          </SettingsSection>

          <SettingsSection
            id="retention"
            label="Data · 04"
            title="History retention"
            icon={Archive}
            description="How long sync runs stay in the timeline, and how often the cleanup sweep runs."
          >
            <RetentionForm />
          </SettingsSection>

          <SettingsSection
            id="cache"
            label="Data · 05"
            title="Binary cache"
            icon={Database}
            description="Per-connector cache of attachments and media. Wipe selectively; some connectors can't re-populate."
          >
            <CacheSection />
          </SettingsSection>

          <SettingsSection
            id="maintenance"
            label="Levers · 06"
            title="Maintenance"
            icon={Wrench}
            description="The big levers. Full re-index, reset all sync cursors."
          >
            <MaintenanceSection />
          </SettingsSection>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Embeddings form
// ---------------------------------------------------------------------------

export function EmbeddingsForm() {
  const ctx = useEmbeddingSettings();

  if (ctx.isPending) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-9 w-2/3 max-w-xl" />
      </div>
    );
  }

  // Remount on every new saved snapshot so useState re-seeds from data
  // instead of the useEffect(setForm(data)) + savedRef pattern that trips
  // the React Compiler rules.
  return (
    <EmbeddingsFormInner
      key={embeddingFingerprint(ctx.data ?? null)}
      ctx={ctx}
    />
  );
}

function ApiKeyField({
  provider,
  apiKey,
  savedApiKey,
  replacingKey,
  onChangeKey,
  onStartReplace,
  onCancelReplace,
}: Readonly<{
  provider: EmbeddingProvider;
  apiKey: string;
  savedApiKey: string;
  replacingKey: boolean;
  onChangeKey: (v: string) => void;
  onStartReplace: () => void;
  onCancelReplace: () => void;
}>) {
  const masked = apiKey?.startsWith("****") && !replacingKey;
  return (
    <Field
      label="API key"
      hint={`Paste your ${providerLabel(provider)} API key. Stored encrypted; only the last four characters show after saving.`}
    >
      {masked ? (
        <div className="flex items-center gap-2">
          <div className="flex h-10 min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-background px-3 font-mono text-[13px] text-muted-foreground">
            <span aria-hidden className="select-none tracking-[0.3em]">
              ••••
            </span>
            <span className="truncate">{apiKey.slice(4)}</span>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onStartReplace}
          >
            Replace
          </Button>
        </div>
      ) : (
        <div className="flex items-center gap-2">
          <Input
            type="password"
            value={apiKey}
            onChange={(e) => onChangeKey(e.target.value)}
            placeholder={apiKeyPlaceholder(provider)}
            className="h-10 flex-1 font-mono text-[13px]"
            autoFocus={replacingKey}
          />
          {replacingKey && savedApiKey && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onCancelReplace}
            >
              Cancel
            </Button>
          )}
        </div>
      )}
    </Field>
  );
}

function embeddingFingerprint(s: EmbeddingSettings | null): string {
  if (!s) return "empty";
  return `${s.provider}|${s.model}|${s.api_key}|${s.ollama_url}`;
}

function EmbeddingsFormInner({ ctx }: Readonly<{ ctx: UseEmbeddingSettings }>) {
  const { data, update } = ctx;
  const saved: EmbeddingSettings = data ?? {
    provider: "",
    model: "",
    api_key: "",
    ollama_url: "http://localhost:11434",
  };

  const stats = useSystemStats();
  const [form, setForm] = useState<EmbeddingSettings>(saved);
  const [replacingKey, setReplacingKey] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);

  const dirtyProvider = form.provider !== saved.provider;
  const dirtyModel = form.model !== saved.model;
  const dirtyOllama = form.ollama_url !== saved.ollama_url;
  const dirtyKey =
    replacingKey && form.api_key !== "" && !form.api_key.startsWith("****");
  const dirty = dirtyProvider || dirtyModel || dirtyOllama || dirtyKey;

  const requiresReindex = dirtyProvider || dirtyModel;

  const needsAPIKey = ["openai", "voyage", "cohere"].includes(form.provider);
  const needsOllama = form.provider === "ollama";

  const totalDocs = stats.data?.total_documents ?? 0;
  const sourceCount = stats.data?.per_source.length ?? 0;

  const handleProviderChange = (next: EmbeddingProvider) => {
    // Returning to the saved provider is a cycle-back from "just peeking"
    // at another provider's options — restore the saved draft so the
    // masked key display reappears and nothing looks destroyed.
    const returningToSaved = saved.provider === next;
    setForm((f) => ({
      ...f,
      provider: next,
      model: returningToSaved
        ? saved.model
        : (DEFAULT_EMBEDDING_MODEL[next] ?? ""),
      // Clearing the key on a real provider switch is deliberate: a
      // voyage key won't authenticate against openai, and the server
      // resolves masked pass-through via the single saved key row, so
      // keeping the masked string here would ship the wrong credential.
      api_key: returningToSaved ? saved.api_key : "",
      ollama_url: returningToSaved ? saved.ollama_url : f.ollama_url,
    }));
    setReplacingKey(false);
  };

  const revert = () => {
    setForm(saved);
    setReplacingKey(false);
  };

  const submit = () => {
    update.mutate(form);
  };

  const onSaveClick = () => {
    if (requiresReindex) setConfirmOpen(true);
    else submit();
  };

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        onSaveClick();
      }}
    >
      <div className="grid max-w-xl gap-5">
        <Field
          label="Provider"
          hint={
            form.provider === ""
              ? "No embeddings — search falls back to BM25 only."
              : "The service that turns documents into vectors."
          }
        >
          <Select
            value={form.provider}
            onValueChange={(v) => handleProviderChange(v as EmbeddingProvider)}
          >
            <SelectTrigger className="h-10 w-full">
              <SelectValue placeholder="Pick a provider" />
            </SelectTrigger>
            <SelectContent>
              {EMBEDDING_PROVIDERS.map((p) => (
                <SelectItem key={p.value || "none"} value={p.value}>
                  <span className="flex items-center gap-2">
                    <span>{p.label}</span>
                    {p.hint && (
                      <span className="text-[11px] text-muted-foreground/80">
                        {p.hint}
                      </span>
                    )}
                  </span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>

        {form.provider !== "" && (
          <Field
            label="Model"
            hint="Pick one of the curated options, or type your own — any provider-valid name works."
          >
            <ModelCombobox
              key={form.provider}
              value={form.model}
              onChange={(v) => setForm((f) => ({ ...f, model: v }))}
              options={EMBEDDING_MODELS[form.provider] ?? []}
            />
          </Field>
        )}

        {needsOllama && (
          <Field
            label="Ollama URL"
            hint="The machine running Ollama. localhost if on the same box."
          >
            <Input
              value={form.ollama_url}
              onChange={(e) =>
                setForm((f) => ({ ...f, ollama_url: e.target.value }))
              }
              placeholder="http://localhost:11434"
              className="h-10 font-mono text-[13px]"
            />
          </Field>
        )}

        {needsAPIKey && (
          <ApiKeyField
            provider={form.provider}
            apiKey={form.api_key}
            savedApiKey={saved.api_key}
            replacingKey={replacingKey}
            onChangeKey={(v) =>
              setForm((f) => ({ ...f, api_key: v }))
            }
            onStartReplace={() => {
              setReplacingKey(true);
              setForm((f) => ({ ...f, api_key: "" }));
            }}
            onCancelReplace={() => {
              setReplacingKey(false);
              setForm((f) => ({ ...f, api_key: saved.api_key }));
            }}
          />
        )}
      </div>

      {requiresReindex && (
        <div className="flex max-w-xl items-start gap-2.5 rounded-md border border-primary/30 bg-primary/5 p-3 text-[13px]">
          <Sparkles
            className="mt-0.5 size-3.5 shrink-0 text-primary"
            aria-hidden
          />
          <div className="flex-1 leading-[1.55]">
            <div className="font-medium text-foreground">
              Saving will trigger a full re-index
            </div>
            <div className="text-muted-foreground">
              {totalDocs > 0 ? (
                <>
                  {totalDocs.toLocaleString()} document
                  {totalDocs === 1 ? "" : "s"} across {sourceCount} source
                  {sourceCount === 1 ? "" : "s"} will be re-embedded. This can
                  take minutes to hours depending on the provider.
                </>
              ) : (
                "The index is empty, so nothing will be re-embedded — new content will use the new provider."
              )}
            </div>
          </div>
        </div>
      )}

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

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <div
              aria-hidden
              className="mb-3 flex size-9 items-center justify-center rounded-full bg-primary/15 text-primary"
            >
              <BellRing className="size-4" />
            </div>
            <DialogTitle>Full re-index?</DialogTitle>
            <DialogDescription>
              Saving will kick off a full re-index
              {totalDocs > 0 ? (
                <>
                  {" "}
                  of{" "}
                  <span className="font-medium text-foreground">
                    {totalDocs.toLocaleString()}
                  </span>{" "}
                  document{totalDocs === 1 ? "" : "s"} across {sourceCount}{" "}
                  source{sourceCount === 1 ? "" : "s"}.
                </>
              ) : (
                " — the index is empty today, so it won't take long."
              )}{" "}
              Search will return incomplete results until it finishes.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => setConfirmOpen(false)}
              disabled={update.isPending}
            >
              Cancel
            </Button>
            <Button
              onClick={() => {
                setConfirmOpen(false);
                submit();
              }}
              disabled={update.isPending}
            >
              {update.isPending ? "Starting…" : "Save & re-index"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </form>
  );
}

function Field({
  label,
  hint,
  children,
}: Readonly<{
  label: string;
  hint?: string;
  children: React.ReactNode;
}>) {
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

// ---------------------------------------------------------------------------
// Model combobox
// ---------------------------------------------------------------------------

interface ModelComboboxProps {
  value: string;
  onChange: (value: string) => void;
  options: ModelOption[];
}

// Parents should `key={provider}` this component so external value resets
// (e.g. provider change in the outer form) remount the combobox instead of
// needing a useEffect(setQuery(value)) sync.
function ModelCombobox({ value, onChange, options }: Readonly<ModelComboboxProps>) {
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
          placeholder="e.g. voyage-4-large"
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
                  {opt.dimension && (
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


// ---------------------------------------------------------------------------
// Mobile section bar — sticky pill at the top of the content area on
// narrow viewports. Tapping opens a Sheet listing every section; tapping
// a row jumps and dismisses. Desktop uses the persistent left aside.
// ---------------------------------------------------------------------------

function MobileSectionsBar({
  active,
  onJump,
}: Readonly<{
  active: string;
  onJump: (id: string) => void;
}>) {
  const [open, setOpen] = useState(false);
  const activeItem = NAV.find((n) => n.id === active) ?? NAV[0];
  const ActiveIcon = activeItem.icon;
  return (
    <div className="md:hidden -mx-2 mb-6">
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex w-full items-center gap-2.5 rounded-full border border-border bg-card px-3.5 py-2 text-left text-[13px] text-foreground transition-colors hover:bg-card-hover"
        aria-haspopup="dialog"
        aria-expanded={open}
      >
        <span className="font-mono text-[10px] tabular-nums text-primary/80">
          {activeItem.ordinal}
        </span>
        <ActiveIcon className="size-3.5 shrink-0 text-primary" aria-hidden />
        <span className="flex-1 truncate">{activeItem.label}</span>
        <ChevronDown
          className={cn("size-3.5 text-muted-foreground transition-transform", open && "rotate-180")}
          aria-hidden
        />
      </button>

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent
          side="bottom"
          className="flex max-h-[80svh] flex-col rounded-t-xl p-0"
        >
          <SheetHeader className="border-b border-border px-4 py-3">
            <SheetTitle className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
              Settings sections
            </SheetTitle>
          </SheetHeader>
          <nav
            aria-label="Settings sections"
            className="flex-1 overflow-y-auto p-2"
          >
            {NAV.map((n) => {
              const Icon = n.icon;
              const isActive = active === n.id;
              return (
                <button
                  key={n.id}
                  type="button"
                  onClick={() => {
                    onJump(n.id);
                    setOpen(false);
                  }}
                  data-active={isActive || undefined}
                  className={cn(
                    "relative flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left text-[14px] transition-colors",
                    "text-muted-foreground hover:bg-accent/40 hover:text-foreground",
                    "data-[active=true]:bg-primary/10 data-[active=true]:text-foreground",
                    "data-[active=true]:before:absolute data-[active=true]:before:inset-y-2 data-[active=true]:before:left-0 data-[active=true]:before:w-[2px] data-[active=true]:before:rounded-full data-[active=true]:before:bg-primary",
                  )}
                >
                  <span className="font-mono text-[10px] tabular-nums text-muted-foreground/60">
                    {n.ordinal}
                  </span>
                  <Icon className="size-4 shrink-0" aria-hidden strokeWidth={2.25} />
                  <span className="flex-1 truncate">{n.label}</span>
                </button>
              );
            })}
          </nav>
        </SheetContent>
      </Sheet>
    </div>
  );
}
