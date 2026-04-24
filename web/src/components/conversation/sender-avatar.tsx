import { memo } from "react";
import { cn } from "@/lib/utils";
import { InitialsAvatar } from "@/components/search/primitives/initials-avatar";

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

  return (
    <InitialsAvatar
      name={senderName}
      seed={seed}
      size={size}
      className={className}
    />
  );
});
