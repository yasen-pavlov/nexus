import { memo } from "react";
import { cn } from "@/lib/utils";
import { hueFor, initialsFor } from "@/components/conversation/avatar-utils";

interface Props {
  /** Display name — drives the initials. Falls back to seed-derived glyph. */
  name?: string | null;
  /** Stable seed for hue/initials fallback (e.g. email address, tag string). */
  seed: string;
  /** Pixel size; defaults to 32. */
  size?: number;
  className?: string;
}

// InitialsAvatar renders a seeded, OKLCH-tinted disc with 1–2 initials.
// Same visual formula as the conversation SenderAvatar fallback — extracted
// here so non-chat cards (email sender, paperless correspondent) can use it
// without pulling in conversation-scoped blob-fetch logic.
export const InitialsAvatar = memo(function InitialsAvatar({
  name,
  seed,
  size = 32,
  className,
}: Readonly<Props>) {
  const displayName = name?.trim() || "Unknown";
  const hue = hueFor(seed);
  const initials = initialsFor(name, seed);

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
