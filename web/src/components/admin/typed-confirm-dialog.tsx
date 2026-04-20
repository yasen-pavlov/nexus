import { type ReactNode, useState } from "react";
import type { LucideIcon } from "lucide-react";
import { TriangleAlert } from "lucide-react";

import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export interface TypedConfirmDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  title: string;
  /** Paragraph body or richer description slot. */
  body: ReactNode;
  /** Phrase the user must type to arm the destructive action. */
  confirmPhrase: string;
  confirmLabel: string;
  /** Muted eyebrow above the title. Defaults to "Danger zone". */
  eyebrow?: string;
  icon?: LucideIcon;
  variant?: "destructive" | "primary";
  onConfirm: () => Promise<void> | void;
}

/**
 * Ceremonial typed-confirm dialog. Matches the connector delete pattern
 * but generalised for the admin surfaces (cache wipe, reindex, etc).
 * The primary variant swaps the destructive hue for marmalade — still a
 * big deal, just not destructive.
 */
export function TypedConfirmDialog({
  open,
  onOpenChange,
  title,
  body,
  confirmPhrase,
  confirmLabel,
  eyebrow = "Danger zone",
  icon: Icon = TriangleAlert,
  variant = "destructive",
  onConfirm,
}: Readonly<TypedConfirmDialogProps>) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const armed = typed === confirmPhrase;

  const chipStyle =
    variant === "destructive"
      ? {
          backgroundColor:
            "color-mix(in oklch, var(--destructive) 14%, transparent)",
          color: "var(--destructive)",
        }
      : {
          backgroundColor:
            "color-mix(in oklch, var(--primary) 16%, transparent)",
          color: "var(--primary)",
        };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setTyped("");
        onOpenChange(next);
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader className="space-y-2">
          <div className="flex items-center gap-3">
            <div
              className="flex h-9 w-9 items-center justify-center rounded-lg"
              style={chipStyle}
            >
              <Icon className="h-4 w-4" aria-hidden />
            </div>
            <div className="min-w-0">
              <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
                {eyebrow}
              </div>
              <DialogTitle>{title}</DialogTitle>
            </div>
          </div>
          <div className="text-[13px] leading-[1.55] text-muted-foreground">
            {body}
          </div>
        </DialogHeader>

        <div className="mt-2 flex flex-col gap-2.5">
          <label className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
            Type{" "}
            <span className="font-mono text-foreground">{confirmPhrase}</span>{" "}
            to confirm
          </label>
          <Input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoFocus
            spellCheck={false}
            placeholder={confirmPhrase}
            className="h-10 font-mono"
          />
        </div>

        <DialogFooter className="gap-2 sm:gap-2">
          <Button
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            variant={variant === "destructive" ? "destructive" : "default"}
            disabled={!armed || busy}
            onClick={async () => {
              setBusy(true);
              try {
                await onConfirm();
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? "Working…" : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
