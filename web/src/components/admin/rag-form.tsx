import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";

import { useRAGSettings, type UseRAGSettings } from "@/hooks/use-rag-settings";
import type { RAGSettings } from "@/lib/api-types";

const MIN_TOOL_ROUNDS = 0;
const MAX_TOOL_ROUNDS = 5;
const MIN_IMAGES = 0;
const MAX_IMAGES = 8;

export function RAGForm() {
  const ctx = useRAGSettings();

  if (ctx.isPending || !ctx.data) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-9 w-full max-w-xl" />
        <Skeleton className="h-12 w-full max-w-xl" />
      </div>
    );
  }

  // Remount when the persisted snapshot changes so the inner form's
  // useState reseeds without an effect — same pattern the other admin
  // forms use to stay React-Compiler-clean.
  return <RAGFormInner key={fingerprint(ctx.data)} ctx={ctx} />;
}

function fingerprint(s: RAGSettings): string {
  return `${s.max_tool_rounds}|${s.max_images_per_turn}|${s.enable_multimodal}|${s.enable_open_attachment}`;
}

function RAGFormInner({ ctx }: Readonly<{ ctx: UseRAGSettings }>) {
  const { data, update } = ctx;
  const saved = data!;

  const [form, setForm] = useState<RAGSettings>({
    max_tool_rounds: saved.max_tool_rounds,
    max_images_per_turn: saved.max_images_per_turn,
    enable_multimodal: saved.enable_multimodal,
    enable_open_attachment: saved.enable_open_attachment,
  });

  const patch = (next: Partial<RAGSettings>) =>
    setForm((f) => ({ ...f, ...next }));

  const dirty =
    form.max_tool_rounds !== saved.max_tool_rounds ||
    form.max_images_per_turn !== saved.max_images_per_turn ||
    form.enable_multimodal !== saved.enable_multimodal ||
    form.enable_open_attachment !== saved.enable_open_attachment;

  const roundsInvalid =
    form.max_tool_rounds < MIN_TOOL_ROUNDS ||
    form.max_tool_rounds > MAX_TOOL_ROUNDS;
  const imagesInvalid =
    form.max_images_per_turn < MIN_IMAGES ||
    form.max_images_per_turn > MAX_IMAGES;
  const invalid = roundsInvalid || imagesInvalid;

  const revert = () =>
    setForm({
      max_tool_rounds: saved.max_tool_rounds,
      max_images_per_turn: saved.max_images_per_turn,
      enable_multimodal: saved.enable_multimodal,
      enable_open_attachment: saved.enable_open_attachment,
    });

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        if (invalid) return;
        update.mutate(form);
      }}
    >
      <div className="grid max-w-xl gap-1.5">
        <Label className="text-[13px] font-medium">
          Max tool rounds per turn
        </Label>
        <Input
          type="number"
          inputMode="numeric"
          aria-label="Max tool rounds per turn"
          min={MIN_TOOL_ROUNDS}
          max={MAX_TOOL_ROUNDS}
          value={form.max_tool_rounds}
          onChange={(e) => patch({ max_tool_rounds: Number(e.target.value) })}
          className={
            roundsInvalid
              ? "h-10 max-w-[120px] border-destructive/60 font-mono text-[13px]"
              : "h-10 max-w-[120px] font-mono text-[13px]"
          }
        />
        <p className="text-[12px] leading-[1.5] text-muted-foreground">
          Higher = the model can chase missing context with follow-up
          searches, at the cost of latency. <code>0</code> disables agentic
          tool calls entirely (single-shot answers from the initial
          retrieval). Capped at {MAX_TOOL_ROUNDS}.
        </p>
      </div>

      {/* Multi-modal section */}
      <div className="flex flex-col gap-4 border-t border-border/60 pt-5">
        <div className="text-[11px] font-semibold uppercase tracking-[0.06em] text-muted-foreground/80">
          Images
        </div>

        <label className="flex max-w-xl cursor-pointer items-start justify-between gap-4">
          <span className="flex flex-col gap-0.5">
            <span className="text-[13px] font-medium">
              Attach images &amp; PDFs to capable models
            </span>
            <span className="text-[12px] leading-[1.5] text-muted-foreground">
              When a retrieved chunk (or its attachment) is a cached image or
              PDF and the chosen model supports it, attach it to the prompt so
              the model can see charts, scans, and pictures — not just the
              extracted text. Anthropic and OpenAI read PDFs natively.
            </span>
          </span>
          <Switch
            checked={form.enable_multimodal}
            onCheckedChange={(v) => patch({ enable_multimodal: v })}
            aria-label="Attach images and PDFs to capable models"
          />
        </label>

        <div className="grid max-w-xl gap-1.5">
          <Label className="text-[13px] font-medium">Max images per turn</Label>
          <Input
            type="number"
            inputMode="numeric"
            aria-label="Max images per turn"
            min={MIN_IMAGES}
            max={MAX_IMAGES}
            value={form.max_images_per_turn}
            disabled={!form.enable_multimodal}
            onChange={(e) =>
              patch({ max_images_per_turn: Number(e.target.value) })
            }
            className={
              imagesInvalid
                ? "h-10 max-w-[120px] border-destructive/60 font-mono text-[13px]"
                : "h-10 max-w-[120px] font-mono text-[13px]"
            }
          />
          <p className="text-[12px] leading-[1.5] text-muted-foreground">
            Cap on cached attachments (images + PDFs, shared) fed to the model
            each turn. <code>0</code> attaches none. Capped at {MAX_IMAGES} to
            keep token cost bounded.
          </p>
        </div>

        <label className="flex max-w-xl cursor-pointer items-start justify-between gap-4">
          <span className="flex flex-col gap-0.5">
            <span className="text-[13px] font-medium">
              Enable the <code>nexus_open_attachment</code> tool
            </span>
            <span className="text-[12px] leading-[1.5] text-muted-foreground">
              Lets the model pull a specific attachment by id mid-answer (image
              for vision models, otherwise its extracted text). Off by default.
            </span>
          </span>
          <Switch
            checked={form.enable_open_attachment}
            onCheckedChange={(v) => patch({ enable_open_attachment: v })}
            aria-label="Enable the nexus_open_attachment tool"
          />
        </label>
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
            <Button type="submit" size="sm" disabled={update.isPending || invalid}>
              {update.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        </div>
      )}
    </form>
  );
}
