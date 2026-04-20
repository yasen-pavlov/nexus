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
    h ^= s.codePointAt(i) ?? 0;
    h = Math.imul(h, 16777619);
  }
  return Math.abs(Math.trunc(h));
}

export function hueFor(seed: string): string {
  return AVATAR_HUES[hashSeed(seed) % AVATAR_HUES.length];
}

export function initialsFor(
  name: string | null | undefined,
  fallbackSeed: string,
): string {
  const n = (name ?? "").trim();
  if (!n) {
    const f = fallbackSeed.replaceAll(/\s+/g, "");
    return (f || "?").slice(0, 2).toUpperCase();
  }
  const parts = n.split(/\s+/).filter(Boolean);
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts.at(-1)![0]).toUpperCase();
}
