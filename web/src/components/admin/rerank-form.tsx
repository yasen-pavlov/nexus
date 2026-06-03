import { useState } from "react";

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
  type RerankProvider,
} from "@/lib/model-catalog";

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

function RerankFormInner({ ctx }: Readonly<{ ctx: UseRerankSettings }>) {
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
              placeholder="e.g. rerank-v3.5"
              showDimensions={false}
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
            {form.api_key?.startsWith("****") && !replacingKey ? (
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

// ModelCombobox is now imported from @/components/ui/model-combobox; the
// rerank form just passes showDimensions={false} since rerank options don't
// carry dimensions.
