import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface Props {
  active: boolean;
  children: ReactNode;
  className?: string;
}

export function AnchorRing({ active, children, className }: Props) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [phase, setPhase] = useState<"intro" | "settled">(
    active ? "intro" : "settled",
  );
  const [scrolled, setScrolled] = useState(false);

  useEffect(() => {
    if (!active) {
      setPhase("settled");
      setScrolled(true);
      return;
    }
    setPhase("intro");
    setScrolled(false);
    const t = window.setTimeout(() => setPhase("settled"), 1800);
    return () => window.clearTimeout(t);
  }, [active]);

  useEffect(() => {
    if (!active || scrolled) return;
    const scroller = ref.current?.closest<HTMLElement>(
      "[data-conversation-scroll]",
    );
    const target: HTMLElement | Window = scroller ?? window;
    const handler = () => setScrolled(true);
    target.addEventListener("scroll", handler, { passive: true });
    return () => target.removeEventListener("scroll", handler);
  }, [active, scrolled]);

  if (!active) {
    return (
      <div ref={ref} className={className}>
        {children}
      </div>
    );
  }

  return (
    <div
      ref={ref}
      data-anchor={scrolled ? "scrolled" : "active"}
      data-phase={phase}
      className={cn(
        "relative rounded-md transition-colors duration-[900ms] ease-out",
        phase === "intro" && "bg-primary/[0.10] ring-2 ring-primary/50",
        phase === "settled" &&
          !scrolled &&
          "bg-primary/[0.04] before:absolute before:top-1 before:bottom-1 before:-left-2 before:w-[2px] before:rounded-full before:bg-primary/80",
        className,
      )}
    >
      {phase === "intro" && (
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 rounded-md bg-primary/[0.08]"
          style={{ animation: "nexus-anchor-glow 1.8s ease-out forwards" }}
        />
      )}
      <div className="relative">{children}</div>
    </div>
  );
}
