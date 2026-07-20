import { useState } from "react";
import { Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ModelCombobox } from "@/components/ui/model-combobox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";

import { useLLMSettings, type UseLLMSettings } from "@/hooks/use-llm-settings";
import { useLLMCatalog } from "@/hooks/use-llm-models";
import type { LLMSettings } from "@/lib/api-types";
import {
  DEFAULT_LLM_BARE_MODEL,
  LLM_BARE_MODELS,
  LLM_PROVIDERS,
  joinModelID,
  splitModelID,
  type LLMProvider,
} from "@/lib/llm-catalog";

/**
 * Admin form for the LLM (RAG answer-generation) layer. Configures Anthropic
 * and OpenAI API keys, the Ollama URL, the default model, and the allowlist
 * of model IDs the per-message picker surfaces. Mirrors the rerank form's
 * UX patterns (masked-key + Replace, sticky save bar, key-on-snapshot
 * remount) so admins don't have to re-learn how settings work.
 */
export function LLMForm() {
  const ctx = useLLMSettings();

  if (ctx.isPending) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-9 w-full max-w-xl" />
      </div>
    );
  }

  return <LLMFormInner key={fingerprint(ctx.data ?? null)} ctx={ctx} />;
}

function fingerprint(s: LLMSettings | null): string {
  if (!s) return "empty";
  return [
    s.default_model,
    s.anthropic_api_key,
    s.openai_api_key,
    s.ollama_url,
    s.allowlist.join(","),
    s.rewriter_model,
  ].join("|");
}

function LLMFormInner({ ctx }: Readonly<{ ctx: UseLLMSettings }>) {
  const { data, update } = ctx;
  const saved: LLMSettings = data ?? {
    default_model: "",
    anthropic_api_key: "",
    openai_api_key: "",
    ollama_url: "",
    allowlist: [],
    rewriter_model: "",
  };

  const [form, setForm] = useState<LLMSettings>(saved);
  const [replacingAnthropic, setReplacingAnthropic] = useState(false);
  const [replacingOpenAI, setReplacingOpenAI] = useState(false);

  // Default model is edited as (provider, bareID); the form joins them on
  // save. Empty provider = "no default" (the orchestrator will fall back).
  const split = splitModelID(form.default_model);
  const defaultProvider: LLMProvider | "" = split.provider;
  const defaultBare = split.bare;

  // Rewriter model uses the same (provider, bareID) edit shape. Empty
  // provider IS the disable — no separate toggle.
  const splitRew = splitModelID(form.rewriter_model);
  const rewriterProvider: LLMProvider | "" = splitRew.provider;
  const rewriterBare = splitRew.bare;

  const dirty =
    form.default_model !== saved.default_model ||
    form.ollama_url !== saved.ollama_url ||
    form.rewriter_model !== saved.rewriter_model ||
    JSON.stringify(form.allowlist) !== JSON.stringify(saved.allowlist) ||
    // A key is dirty when it differs from the saved value and isn't still the
    // mask. Do NOT gate on the `replacing*` flag: a first-time key (saved value
    // is "") renders a plain input with replacing=false, so gating there meant
    // a pasted first key never marked the form dirty and the Save bar never
    // appeared.
    (form.anthropic_api_key !== saved.anthropic_api_key &&
      !form.anthropic_api_key.startsWith("****")) ||
    (form.openai_api_key !== saved.openai_api_key &&
      !form.openai_api_key.startsWith("****"));

  const handleDefaultProviderChange = (next: LLMProvider | "") => {
    if (next === "") {
      setForm((f) => ({ ...f, default_model: "" }));
      return;
    }
    const bare = DEFAULT_LLM_BARE_MODEL[next] ?? "";
    setForm((f) => ({ ...f, default_model: joinModelID(next, bare) }));
  };

  const handleDefaultBareChange = (bare: string) => {
    if (defaultProvider === "") return;
    setForm((f) => ({
      ...f,
      default_model: bare ? joinModelID(defaultProvider, bare) : "",
    }));
  };

  const handleRewriterProviderChange = (next: LLMProvider | "") => {
    if (next === "") {
      setForm((f) => ({ ...f, rewriter_model: "" }));
      return;
    }
    const bare = DEFAULT_LLM_BARE_MODEL[next] ?? "";
    setForm((f) => ({ ...f, rewriter_model: joinModelID(next, bare) }));
  };

  const handleRewriterBareChange = (bare: string) => {
    if (rewriterProvider === "") return;
    setForm((f) => ({
      ...f,
      rewriter_model: bare ? joinModelID(rewriterProvider, bare) : "",
    }));
  };

  const revert = () => {
    setForm(saved);
    setReplacingAnthropic(false);
    setReplacingOpenAI(false);
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
          label="Default model"
          hint="Used when a chat doesn't override its model. Pick the provider first; pick or type the model id second."
        >
          <div className="grid grid-cols-[200px_1fr] gap-2">
            <Select
              value={defaultProvider}
              onValueChange={(v) =>
                handleDefaultProviderChange(v as LLMProvider | "")
              }
            >
              <SelectTrigger className="h-10">
                <SelectValue placeholder="Pick a provider" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">No default</SelectItem>
                {LLM_PROVIDERS.map((p) => (
                  <SelectItem key={p.value} value={p.value}>
                    {p.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {defaultProvider !== "" && (
              <ModelCombobox
                key={defaultProvider}
                value={defaultBare}
                onChange={handleDefaultBareChange}
                options={LLM_BARE_MODELS[defaultProvider] ?? []}
                placeholder={
                  DEFAULT_LLM_BARE_MODEL[defaultProvider] ?? "model id"
                }
                showDimensions={false}
              />
            )}
          </div>
        </Field>

        {/* Refinement — optional cheap-model used for query rewriting +
            auto-titles. Hairline divider with uppercase label is the
            "you're entering optional territory" mark; the field row
            itself mirrors the Default-model row exactly so muscle
            memory transfers. Empty provider = disabled. */}
        <div className="mt-1 flex items-center gap-3 border-t border-border/50 pt-3">
          <span className="text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/70">
            Refinement
          </span>
          <span className="h-px flex-1 bg-border/40" aria-hidden />
        </div>

        <Field
          label="Rewriter model"
          hint="Cheap model used to refine multi-turn questions before search and to summarise chats into titles. Pick something fast and inexpensive — Haiku, GPT-mini, or a small Ollama model. Leave the provider unset to disable refinement."
        >
          <div className="grid grid-cols-[200px_1fr] gap-2">
            <Select
              value={rewriterProvider}
              onValueChange={(v) =>
                handleRewriterProviderChange(v as LLMProvider | "")
              }
            >
              <SelectTrigger className="h-10">
                <SelectValue placeholder="Pick a provider" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">Disabled</SelectItem>
                {LLM_PROVIDERS.map((p) => (
                  <SelectItem key={p.value} value={p.value}>
                    {p.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {rewriterProvider !== "" && (
              <ModelCombobox
                key={rewriterProvider}
                value={rewriterBare}
                onChange={handleRewriterBareChange}
                options={LLM_BARE_MODELS[rewriterProvider] ?? []}
                placeholder={
                  DEFAULT_LLM_BARE_MODEL[rewriterProvider] ?? "model id"
                }
                showDimensions={false}
              />
            )}
          </div>
        </Field>

        <ApiKeyField
          label="Anthropic API key"
          providerName="Anthropic"
          value={form.anthropic_api_key}
          replacing={replacingAnthropic}
          onChange={(v) =>
            setForm((f) => ({ ...f, anthropic_api_key: v }))
          }
          onStartReplace={() => {
            setReplacingAnthropic(true);
            setForm((f) => ({ ...f, anthropic_api_key: "" }));
          }}
          onCancelReplace={() => {
            setReplacingAnthropic(false);
            setForm((f) => ({ ...f, anthropic_api_key: saved.anthropic_api_key }));
          }}
        />

        <ApiKeyField
          label="OpenAI API key"
          providerName="OpenAI"
          value={form.openai_api_key}
          replacing={replacingOpenAI}
          onChange={(v) =>
            setForm((f) => ({ ...f, openai_api_key: v }))
          }
          onStartReplace={() => {
            setReplacingOpenAI(true);
            setForm((f) => ({ ...f, openai_api_key: "" }));
          }}
          onCancelReplace={() => {
            setReplacingOpenAI(false);
            setForm((f) => ({ ...f, openai_api_key: saved.openai_api_key }));
          }}
        />

        <Field
          label="Ollama URL"
          hint="Where your local Ollama runs. Leave blank to disable. Falls back to the shared NEXUS_OLLAMA_URL env var when empty."
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

        <AllowlistField
          allowlist={form.allowlist}
          onChange={(next) => setForm((f) => ({ ...f, allowlist: next }))}
        />
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

// ApiKeyField mirrors the rerank/embedding masked-key UX: when the saved key
// is masked ("****abcd") show the obfuscated tail + a Replace button; on
// Replace clear the field and let the user paste a new plaintext key.
function ApiKeyField({
  label,
  providerName,
  value,
  replacing,
  onChange,
  onStartReplace,
  onCancelReplace,
}: Readonly<{
  label: string;
  providerName: string;
  value: string;
  replacing: boolean;
  onChange: (v: string) => void;
  onStartReplace: () => void;
  onCancelReplace: () => void;
}>) {
  return (
    <Field
      label={label}
      hint={`Paste your ${providerName} API key. Stored encrypted at rest; only the last four characters show after saving. Leave blank to disable ${providerName}.`}
    >
      {value?.startsWith("****") && !replacing ? (
        <div className="flex items-center gap-2">
          <div className="flex h-10 min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-background px-3 font-mono text-[13px] text-muted-foreground">
            <span aria-hidden className="select-none tracking-[0.3em]">
              ••••
            </span>
            <span className="truncate">{value.slice(4)}</span>
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
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="paste your key or leave blank"
            className="h-10 flex-1 font-mono text-[13px]"
            autoFocus={replacing}
          />
          {replacing && (
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

// AllowlistField renders one toggle row per configured-provider model.
// Pulls the PRE-allowlist catalog so deselecting a model leaves the row
// in place (otherwise unticking would remove the row from its own editor
// and the admin couldn't re-tick it). Empty allowlist = expose every
// model whose provider has a key.
function AllowlistField({
  allowlist,
  onChange,
}: Readonly<{
  allowlist: string[];
  onChange: (next: string[]) => void;
}>) {
  const models = useLLMCatalog();

  const isAllowed = (id: string) =>
    allowlist.length === 0 || allowlist.includes(id);

  const toggle = (id: string) => {
    if (allowlist.length === 0) {
      // Switching from "expose all" to a concrete allowlist — start with
      // every model except the one being toggled off.
      const remaining = (models.data ?? [])
        .map((m) => m.id)
        .filter((mid) => mid !== id);
      onChange(remaining);
      return;
    }
    if (allowlist.includes(id)) {
      onChange(allowlist.filter((mid) => mid !== id));
    } else {
      onChange([...allowlist, id]);
    }
  };

  return (
    <Field
      label="Model allowlist"
      hint="Models the per-message picker offers. Leave every row checked to expose every model from the providers you configured above."
    >
      {models.isPending && <Skeleton className="h-20 w-full" />}
      {!models.isPending && (models.data ?? []).length === 0 && (
        <div className="flex items-start gap-2.5 rounded-md border border-dashed border-border bg-muted/30 px-3 py-2.5 text-[13px] text-muted-foreground">
          <Sparkles className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/70" aria-hidden />
          <span>
            Configure a provider above and save — once a key is set, the
            catalog will appear here so you can fine-tune which models users
            see.
          </span>
        </div>
      )}
      {!models.isPending && (models.data ?? []).length > 0 && (
        <ul className="grid gap-1 rounded-md border border-border bg-background px-1.5 py-1.5">
          {(models.data ?? []).map((m) => (
            <li key={m.id}>
              <label className="flex cursor-pointer items-center gap-2.5 rounded-sm px-2 py-1.5 transition-colors hover:bg-muted/60">
                <input
                  type="checkbox"
                  checked={isAllowed(m.id)}
                  onChange={() => toggle(m.id)}
                  aria-label={m.display_name}
                  className="size-4 accent-primary"
                />
                <span className="flex-1 truncate font-mono text-[13px] tabular-nums">
                  {m.id}
                </span>
                <span className="flex items-center gap-1 text-[10.5px] text-muted-foreground">
                  {m.supports_vision && <Pill>vision</Pill>}
                  {m.supports_tools && <Pill>tools</Pill>}
                  {m.supports_citations && <Pill>citations</Pill>}
                </span>
              </label>
            </li>
          ))}
        </ul>
      )}
    </Field>
  );
}

function Pill({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground/90">
      {children}
    </span>
  );
}
