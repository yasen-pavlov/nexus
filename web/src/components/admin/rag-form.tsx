import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";

import { useRAGSettings, type UseRAGSettings } from "@/hooks/use-rag-settings";
import type { RAGSettings } from "@/lib/api-types";

const MIN_TOOL_ROUNDS = 0;
const MAX_TOOL_ROUNDS = 5;

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
  return `${s.max_tool_rounds}`;
}

function RAGFormInner({ ctx }: Readonly<{ ctx: UseRAGSettings }>) {
  const { data, update } = ctx;
  const saved = data!;

  const [form, setForm] = useState<RAGSettings>({
    max_tool_rounds: saved.max_tool_rounds,
  });

  const dirty = form.max_tool_rounds !== saved.max_tool_rounds;
  const invalid =
    form.max_tool_rounds < MIN_TOOL_ROUNDS ||
    form.max_tool_rounds > MAX_TOOL_ROUNDS;

  const revert = () => setForm({ max_tool_rounds: saved.max_tool_rounds });

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
          min={MIN_TOOL_ROUNDS}
          max={MAX_TOOL_ROUNDS}
          value={form.max_tool_rounds}
          onChange={(e) =>
            setForm({ max_tool_rounds: Number(e.target.value) })
          }
          className={
            invalid
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
            <Button
              type="submit"
              size="sm"
              disabled={update.isPending || invalid}
            >
              {update.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        </div>
      )}
    </form>
  );
}
