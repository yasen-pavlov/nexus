import { formatDistanceToNow, parseISO } from "date-fns";

// Byte formatter. Reading-room voice: avoid KB/MB/GB per-row inconsistency —
// pick the unit whose value lands in [0.1, 1000) so the table reads cleanly.
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const decimals = value < 10 && unit > 0 ? 1 : 0;
  return `${value.toFixed(decimals)} ${units[unit]}`;
}

// Human-relative timestamp. Falls back to em dash when the input is empty.
// Wraps parseISO so components don't duplicate the null-guard per usage.
export function formatRelative(iso: string | undefined | null): string {
  if (!iso) return "—";
  try {
    return formatDistanceToNow(parseISO(iso), { addSuffix: true });
  } catch {
    return iso;
  }
}

// Absolute timestamp. Used as the tooltip companion of formatRelative.
export function formatAbsolute(iso: string | undefined | null): string {
  if (!iso) return "—";
  try {
    return parseISO(iso).toLocaleString();
  } catch {
    return iso;
  }
}

// Compact number formatter (2,401 → "2,401" vs "2.4k"). Right now we use the
// locale's thousands separator without abbreviation — the values live in
// right-aligned columns where the full number reads clean at 13-13.5px.
export function formatCount(n: number): string {
  if (!Number.isFinite(n)) return "—";
  return n.toLocaleString();
}
