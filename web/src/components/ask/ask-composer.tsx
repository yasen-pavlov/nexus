import { useEffect, useRef, useState } from "react";
import { ArrowUp, Square } from "lucide-react";

import { Button } from "@/components/ui/button";
import type { LLMModelInfo } from "@/lib/api-types";
import { cn } from "@/lib/utils";

import { ModelPickerChip } from "./model-picker-chip";

export interface AskComposerProps {
  model: string;
  onModelChange: (m: string) => void;
  models: LLMModelInfo[];
  onSubmit: (content: string) => void;
  isStreaming?: boolean;
  onCancel?: () => void;
  isFirstTurn?: boolean;
  initialContent?: string;
}

const MIN_HEIGHT_PX = 56;
const MAX_HEIGHT_PX = 192;
const isMac = (): boolean =>
  typeof navigator !== "undefined" && /Mac/i.test(navigator.platform);

/**
 * Multi-line composer for the Ask surface. Cmd/Ctrl+Enter submits;
 * plain Enter inserts a newline. Esc cancels mid-stream (or blurs when
 * idle). Footer carries the model picker chip and the send/stop button.
 */
export function AskComposer({
  model,
  onModelChange,
  models,
  onSubmit,
  isStreaming = false,
  onCancel,
  isFirstTurn = false,
  initialContent = "",
}: Readonly<AskComposerProps>) {
  const [value, setValue] = useState(initialContent);
  const ref = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(MAX_HEIGHT_PX, Math.max(MIN_HEIGHT_PX, el.scrollHeight))}px`;
  }, [value]);

  useEffect(() => {
    ref.current?.focus();
  }, []);

  const send = () => {
    const trimmed = value.trim();
    if (!trimmed || isStreaming) return;
    onSubmit(trimmed);
    setValue("");
  };

  const handleKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      send();
      return;
    }
    if (e.key === "Escape") {
      if (isStreaming) {
        e.preventDefault();
        onCancel?.();
      } else {
        ref.current?.blur();
      }
    }
  };

  const placeholder = isFirstTurn
    ? "Ask a follow-up…"
    : "Ask anything — your email, Telegram, files…";
  const hasContent = value.trim().length > 0;
  const showHint = hasContent && !isStreaming;

  return (
    <div
      className={cn(
        "rounded-xl border border-input bg-card shadow-xs transition-[border-color,box-shadow]",
        "focus-within:border-ring focus-within:ring-4 focus-within:ring-ring/15",
      )}
    >
      <textarea
        ref={ref}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={handleKey}
        placeholder={placeholder}
        disabled={isStreaming}
        rows={2}
        className={cn(
          "block w-full resize-none rounded-t-xl bg-transparent px-4 pt-3 pb-2 text-[15px] leading-[22px] tracking-[-0.005em] outline-none",
          "placeholder:text-muted-foreground/70",
          isStreaming && "opacity-70",
        )}
        style={{ minHeight: `${MIN_HEIGHT_PX}px`, maxHeight: `${MAX_HEIGHT_PX}px` }}
      />
      <div className="flex items-center gap-2 border-t border-border/60 px-3 py-2">
        <ModelPickerChip value={model} onChange={onModelChange} models={models} />
        <div className="flex-1" />
        {showHint && (
          <span className="hidden text-[10px] text-muted-foreground/70 sm:inline-flex sm:items-center sm:gap-1">
            <kbd className="rounded border border-border/60 bg-muted px-1 py-px font-medium">
              {isMac() ? "⌘" : "Ctrl"}
            </kbd>
            <kbd className="rounded border border-border/60 bg-muted px-1 py-px font-medium">↵</kbd>
            <span>to send</span>
          </span>
        )}
        {isStreaming ? (
          <Button
            type="button"
            size="icon"
            onClick={onCancel}
            aria-label="Cancel"
            className="size-8 rounded-lg border border-destructive/30 bg-destructive/15 text-destructive hover:bg-destructive/20"
            variant="ghost"
          >
            <Square className="size-3.5 fill-current" aria-hidden />
          </Button>
        ) : (
          <Button
            type="button"
            size="icon"
            onClick={send}
            disabled={!hasContent || !model}
            aria-label="Send"
            className="size-8 rounded-lg"
          >
            <ArrowUp className="size-4" aria-hidden />
          </Button>
        )}
      </div>
    </div>
  );
}
