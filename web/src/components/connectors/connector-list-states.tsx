import { PackagePlus, PlugZap } from "lucide-react";

import { Button } from "@/components/ui/button";

export function ConnectorsLoading() {
  return (
    <ul className="flex flex-col gap-3" aria-label="Loading connectors">
      {[0, 1, 2].map((i) => (
        <li
          key={i}
          className="h-[88px] animate-pulse overflow-hidden rounded-lg border border-border bg-card"
          style={{ animationDelay: `${i * 80}ms` }}
        >
          <div className="flex items-center gap-4 p-4 pl-5">
            <div className="h-10 w-10 rounded-lg bg-muted" />
            <div className="flex-1 space-y-2">
              <div className="h-3 w-1/3 rounded-full bg-muted" />
              <div className="h-2 w-2/3 rounded-full bg-muted/60" />
            </div>
          </div>
        </li>
      ))}
    </ul>
  );
}

/**
 * First-boot state. Stippled dot grid backdrop (masked at the edges) hints
 * at the "workbench" metaphor without being loud. Single primary CTA
 * opens the create sheet — no secondary distractions.
 */
export function ConnectorsEmpty({ onAdd }: Readonly<{ onAdd: () => void }>) {
  return (
    <div className="relative overflow-hidden rounded-xl border border-border bg-card px-8 py-16 text-center">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 opacity-[0.45]"
        style={{
          backgroundImage:
            "radial-gradient(circle at 1px 1px, color-mix(in oklch, var(--border) 90%, transparent) 1px, transparent 0)",
          backgroundSize: "24px 24px",
          maskImage: "radial-gradient(ellipse at center, black 30%, transparent 70%)",
        }}
      />
      <div className="relative mx-auto flex max-w-sm flex-col items-center gap-4">
        <div
          className="flex h-12 w-12 items-center justify-center rounded-xl"
          style={{
            backgroundColor: "color-mix(in oklch, var(--primary) 14%, transparent)",
            color: "var(--primary)",
          }}
        >
          <PlugZap className="h-5 w-5" strokeWidth={1.75} />
        </div>
        <div>
          <h2 className="text-[16px] font-medium tracking-[-0.005em] text-foreground">
            Your workbench is empty
          </h2>
          <p className="mt-1 text-[13.5px] text-muted-foreground">
            Connectors pull your email, chats, documents, and files into a single search. Start
            with one — you can always add more.
          </p>
        </div>
        <Button onClick={onAdd} className="gap-2">
          <PackagePlus className="h-4 w-4" />
          Add your first connector
        </Button>
      </div>
    </div>
  );
}
