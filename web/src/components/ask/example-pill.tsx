import { cn } from "@/lib/utils";

export interface ExamplePillProps {
  prompt: string;
  onPick: (p: string) => void;
}

/**
 * Visually-restrained pill the landing surface uses to seed the
 * composer with a starter prompt. Suggestions, not the protagonist —
 * neutral border, neutral fill, marmalade only on hover.
 */
export function ExamplePill({ prompt, onPick }: Readonly<ExamplePillProps>) {
  return (
    <button
      type="button"
      onClick={() => onPick(prompt)}
      className={cn(
        "inline-flex h-7 items-center rounded-full border border-border bg-card px-3 text-[12px] font-medium text-foreground/80 transition-colors",
        "hover:bg-card-hover hover:border-primary/30 hover:text-foreground",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
      )}
    >
      {prompt}
    </button>
  );
}
