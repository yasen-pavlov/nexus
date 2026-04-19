import { memo } from "react";
import { cn } from "@/lib/utils";
import { hueFor, initialsFor } from "./avatar-utils";

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
