import { KeyRound } from "lucide-react";

import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

export interface ShortcutsSheetProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}

interface Shortcut {
  label: string;
  /** keys to display in order, e.g. ["g", "s"] for chords or ["⌘", "K"] for combos */
  keys: string[];
  /** shown muted next to the keys */
  note?: string;
}

interface Section {
  title: string;
  rows: Shortcut[];
}

const SECTIONS: Section[] = [
  {
    title: "Navigation",
    rows: [
      { label: "Go to Search", keys: ["g", "s"] },
      { label: "Go to Connectors", keys: ["g", "c"] },
      { label: "Go to Admin Settings", keys: ["g", "a"], note: "admin only" },
    ],
  },
  {
    title: "Search",
    rows: [{ label: "Focus search input", keys: ["/"] }],
  },
  {
    title: "Application",
    rows: [
      { label: "Command palette", keys: ["⌘", "K"], note: "or Ctrl+K" },
      { label: "Toggle sidebar", keys: ["⌘", "B"], note: "or Ctrl+B" },
      { label: "Open this sheet", keys: ["?"] },
      { label: "Dismiss any overlay", keys: ["Esc"] },
    ],
  },
];

/**
 * Keyboard-shortcut cheat sheet. Despite the name "sheet", uses Dialog
 * for the cheat-sheet metaphor — small, centered, designed to be glanced
 * at and dismissed with the same key that opened it.
 */
export function ShortcutsSheet({ open, onOpenChange }: ShortcutsSheetProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[440px]">
        <header className="flex items-center gap-3">
          <div
            aria-hidden
            className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary"
          >
            <KeyRound className="size-4" />
          </div>
          <div className="min-w-0">
            <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
              Reference
            </div>
            <DialogTitle className="text-[15px] font-medium">
              Keyboard shortcuts
            </DialogTitle>
          </div>
        </header>

        <div className="-mx-1 mt-1 flex flex-col gap-5">
          {SECTIONS.map((s) => (
            <section key={s.title} className="flex flex-col gap-1.5">
              <h3 className="px-1 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/70">
                {s.title}
              </h3>
              <div className="flex flex-col">
                {s.rows.map((row, idx) => (
                  <div
                    key={`${s.title}-${idx}`}
                    className={cn(
                      "flex items-center justify-between gap-3 rounded-md px-1 py-1.5",
                      "hover:bg-accent/30",
                    )}
                  >
                    <div className="flex min-w-0 flex-1 items-baseline gap-2 leading-tight">
                      <span className="truncate text-[13px] text-foreground/90">
                        {row.label}
                      </span>
                      {row.note && (
                        <span className="truncate text-[11.5px] text-muted-foreground/80">
                          {row.note}
                        </span>
                      )}
                    </div>
                    <span className="flex shrink-0 items-center gap-1">
                      {row.keys.map((k, i) => (
                        <KbdChip key={`${k}-${i}`} keyLabel={k} />
                      ))}
                    </span>
                  </div>
                ))}
              </div>
            </section>
          ))}
        </div>

        <footer className="-mx-4 -mb-4 mt-2 rounded-b-xl border-t border-border bg-muted/40 px-4 py-2.5 text-[11.5px] leading-[1.5] text-muted-foreground/80">
          <span aria-hidden className="mr-1.5 text-primary/70">
            ◆
          </span>
          Tip: chord shortcuts (
          <KbdChip keyLabel="g" inline /> + key) need to be pressed within
          200&nbsp;ms.
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function KbdChip({
  keyLabel,
  inline = false,
}: {
  keyLabel: string;
  inline?: boolean;
}) {
  return (
    <kbd
      className={cn(
        "inline-flex min-w-[20px] items-center justify-center rounded-md border border-border bg-muted px-1.5 font-mono text-[11px] leading-none text-foreground/85",
        inline ? "h-4 -translate-y-px text-[10px]" : "h-5",
      )}
    >
      {keyLabel}
    </kbd>
  );
}
