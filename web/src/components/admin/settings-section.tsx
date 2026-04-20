import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";

interface SettingsSectionProps {
  id: string;
  label: string;
  title: string;
  description?: string;
  icon?: LucideIcon;
  actions?: ReactNode;
  bare?: boolean;
  children: ReactNode;
}

/**
 * Anchored section primitive for the admin Settings page.
 *
 * Composition: reading-room eyebrow (hairline accent + optional icon +
 * uppercase label) → title → optional description → body wrapped in the
 * standard `bg-card` container (skip via `bare`).
 *
 * `scroll-mt-20` lets in-page anchor navigation land the heading
 * comfortably below the top bar rather than directly at the viewport top.
 */
export function SettingsSection({
  id,
  label,
  title,
  description,
  icon: Icon,
  actions,
  bare = false,
  children,
}: Readonly<SettingsSectionProps>) {
  return (
    <section id={id} className="scroll-mt-20">
      <header className="mb-4 flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="mb-2 flex items-center gap-2.5">
            <span aria-hidden className="h-[2px] w-6 rounded-full bg-primary/35" />
            {Icon && (
              <Icon
                className="size-3 shrink-0 text-muted-foreground/70"
                aria-hidden
                strokeWidth={2.25}
              />
            )}
            <span className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
              {label}
            </span>
          </div>
          <h2 className="text-[17px] font-medium tracking-[-0.005em] text-foreground">
            {title}
          </h2>
          {description && (
            <p className="mt-1 max-w-prose text-[13.5px] leading-[1.55] text-muted-foreground">
              {description}
            </p>
          )}
        </div>
        {actions && <div className="shrink-0 pt-1">{actions}</div>}
      </header>

      {bare ? (
        <div>{children}</div>
      ) : (
        <div
          className={cn(
            "relative rounded-lg border border-border bg-card p-6",
            "transition-colors",
          )}
        >
          {children}
        </div>
      )}
    </section>
  );
}
