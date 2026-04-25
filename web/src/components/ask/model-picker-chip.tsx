import { useState } from "react";
import { ChevronDown, Sparkles } from "lucide-react";

import { ModelCombobox } from "@/components/ui/model-combobox";
import type { LLMModelInfo } from "@/lib/api-types";
import { splitModelID } from "@/lib/llm-catalog";
import { cn } from "@/lib/utils";

export interface ModelPickerChipProps {
  value: string;
  onChange: (m: string) => void;
  models: LLMModelInfo[];
}

const LABEL_TRUNCATE = 24;

function priceHint(m: LLMModelInfo): string {
  return `$${m.input_cost_per_mtok.toFixed(2)} / 1M in · $${m.output_cost_per_mtok.toFixed(2)} / 1M out`;
}

function deriveLabel(value: string, models: LLMModelInfo[]): string {
  const match = models.find((m) => m.id === value);
  const raw = match?.display_name ?? splitModelID(value).bare ?? value;
  return raw.length > LABEL_TRUNCATE ? raw.slice(0, LABEL_TRUNCATE - 1) + "…" : raw;
}

/**
 * Marmalade pill that opens a popover with the shared ModelCombobox.
 * Lives in the AskComposer footer and on the message-level override
 * surface inside chat turns.
 */
export function ModelPickerChip({ value, onChange, models }: Readonly<ModelPickerChipProps>) {
  const [open, setOpen] = useState(false);
  const selected = models.find((m) => m.id === value);
  const label = deriveLabel(value, models);

  const options = models.map((m) => ({
    value: m.id,
    label: m.display_name,
    notes: priceHint(m),
  }));

  return (
    <div className="relative">
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={`Model: ${label}`}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "inline-flex h-7 items-center gap-1.5 rounded-full border border-primary/25 bg-primary/10 px-2.5 text-[12px] font-medium text-primary transition-colors",
          "hover:bg-primary/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
        )}
      >
        <Sparkles className="size-3" aria-hidden />
        <span className="max-w-[14rem] truncate">{label}</span>
        <ChevronDown className="size-3 opacity-70" aria-hidden />
      </button>

      {open && (
        <>
          <button
            type="button"
            aria-label="Close model picker"
            onClick={() => setOpen(false)}
            className="fixed inset-0 z-40 cursor-default"
          />
          <div
            role="dialog"
            aria-label="Pick model"
            className="absolute bottom-full left-0 z-50 mb-2 w-[320px] rounded-lg border border-border bg-popover p-3 shadow-sm"
          >
            <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
              Model
            </div>
            <div className="mt-2">
              <ModelCombobox
                value={value}
                onChange={(v) => {
                  onChange(v);
                  setOpen(false);
                }}
                options={options}
                showDimensions={false}
                placeholder="Search models…"
              />
            </div>
            {selected && (
              <div className="mt-3 flex flex-wrap gap-1.5 text-[10px]">
                <CapPill label={`${(selected.context_window / 1000).toFixed(0)}k ctx`} />
                {selected.supports_citations && <CapPill label="cites" />}
                {selected.supports_vision && <CapPill label="vision" />}
                {selected.supports_caching && <CapPill label="cached" />}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

function CapPill({ label }: Readonly<{ label: string }>) {
  return (
    <span className="rounded-full bg-muted px-1.5 py-0.5 font-medium uppercase tracking-[0.08em] text-muted-foreground">
      {label}
    </span>
  );
}
