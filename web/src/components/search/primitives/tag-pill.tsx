import { cn } from "@/lib/utils";

const TAG_HUES = [
  "oklch(0.58 0.09 65)",
  "oklch(0.55 0.09 155)",
  "oklch(0.54 0.10 255)",
  "oklch(0.54 0.11 335)",
  "oklch(0.56 0.07 100)",
  "oklch(0.56 0.09 210)",
  "oklch(0.55 0.10 30)",
  "oklch(0.55 0.08 125)",
];

function hashSeed(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.codePointAt(i) ?? 0;
    h = Math.imul(h, 16777619);
  }
  return Math.abs(Math.trunc(h));
}

interface Props {
  label: string;
  className?: string;
  title?: string;
}

// TagPill renders a single tag as a seeded-hue capsule. Same OKLCH
// blend formula as SourceChip's pill variant so tags feel like part
// of the same visual system.
export function TagPill({ label, className, title }: Readonly<Props>) {
  const hue = TAG_HUES[hashSeed(label) % TAG_HUES.length];
  const style = {
    "--tag-hue": hue,
  } as React.CSSProperties;

  return (
    <span
      style={style}
      title={title ?? label}
      className={cn(
        "inline-flex h-5 shrink-0 items-center rounded-full border px-2 text-[11.5px] font-medium leading-none select-none",
        "border-[color-mix(in_oklch,var(--tag-hue)_30%,transparent)]",
        "bg-[color-mix(in_oklch,var(--tag-hue)_12%,transparent)]",
        "text-[color-mix(in_oklch,var(--tag-hue)_70%,var(--foreground))]",
        className,
      )}
    >
      {label}
    </span>
  );
}
