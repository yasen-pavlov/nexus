import { memo } from "react";
import { cn } from "@/lib/utils";

const AVATAR_HUES = [
  "oklch(0.62 0.10 65)",
  "oklch(0.58 0.09 155)",
  "oklch(0.55 0.09 255)",
  "oklch(0.55 0.09 335)",
  "oklch(0.58 0.06 100)",
  "oklch(0.52 0.02 260)",
];

function hashSeed(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return Math.abs(h | 0);
}

function hueFor(seed: string): string {
  return AVATAR_HUES[hashSeed(seed) % AVATAR_HUES.length]!;
}

function initialsFor(name: string | null | undefined, fallbackSeed: string): string {
  const n = (name ?? "").trim();
  if (!n) {
    const f = fallbackSeed.replace(/\s+/g, "");
    return (f || "?").slice(0, 2).toUpperCase();
  }
  const parts = n.split(/\s+/).filter(Boolean);
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]![0]! + parts[parts.length - 1]![0]!).toUpperCase();
}

interface Props {
  blobUrl?: string | null;
  senderName?: string | null;
  seed: string;
  size?: number;
  className?: string;
}

export const SenderAvatar = memo(function SenderAvatar({
  blobUrl,
  senderName,
  seed,
  size = 32,
  className,
}: Props) {
  const displayName = senderName?.trim() || "Unknown";

  if (blobUrl) {
    return (
      <img
        src={blobUrl}
        alt={displayName}
        width={size}
        height={size}
        loading="lazy"
        className={cn("shrink-0 rounded-full object-cover", className)}
        style={{ width: size, height: size }}
      />
    );
  }

  const hue = hueFor(seed);
  const initials = initialsFor(senderName, seed);

  return (
    <div
      role="img"
      aria-label={displayName}
      className={cn(
        "flex shrink-0 items-center justify-center rounded-full font-medium tracking-[-0.01em] select-none",
        className,
      )}
      style={{
        width: size,
        height: size,
        fontSize: Math.max(11, Math.round(size * 0.38)),
        backgroundColor: `color-mix(in oklch, ${hue} 22%, var(--muted))`,
        color: `color-mix(in oklch, ${hue} 65%, var(--foreground))`,
      }}
    >
      {initials}
    </div>
  );
});

export { initialsFor, hueFor };
