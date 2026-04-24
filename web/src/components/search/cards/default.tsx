import { Fragment } from "react";
import type { DocumentHit } from "@/lib/api-types";

function isScalar(v: unknown): v is string | number | boolean {
  return (
    typeof v === "string" || typeof v === "number" || typeof v === "boolean"
  );
}

// DefaultCardBody is the fallback for unknown source types. It renders
// scalar metadata as a compact two-column key/value grid. Intentionally
// minimal — the typography scale matches the other redesigned cards, but
// the composition stays a grid because we don't know what data a future
// source will carry.
export function DefaultCardBody({ hit }: Readonly<{ hit: DocumentHit }>) {
  const entries = Object.entries(hit.metadata ?? {}).filter(
    (e): e is [string, string | number | boolean] =>
      isScalar(e[1]) && String(e[1]).length > 0,
  );

  if (entries.length === 0) return null;

  return (
    <dl className="mt-2.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12.5px]">
      {entries.slice(0, 6).map(([key, value]) => (
        <Fragment key={key}>
          <dt className="text-[11.5px] font-medium uppercase tracking-wide text-muted-foreground/80">
            {key}
          </dt>
          <dd className="truncate text-foreground/90 tabular-nums">
            {String(value)}
          </dd>
        </Fragment>
      ))}
    </dl>
  );
}
