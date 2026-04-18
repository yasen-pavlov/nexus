import { Fragment } from "react";
import type { DocumentHit } from "@/lib/api-types";

function isScalar(v: unknown): v is string | number | boolean {
  return (
    typeof v === "string" || typeof v === "number" || typeof v === "boolean"
  );
}

export function DefaultCardBody({ hit }: { hit: DocumentHit }) {
  const entries = Object.entries(hit.metadata ?? {}).filter(
    (e): e is [string, string | number | boolean] =>
      isScalar(e[1]) && String(e[1]).length > 0,
  );

  if (entries.length === 0) return null;

  return (
    <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12.5px]">
      {entries.slice(0, 6).map(([key, value]) => (
        <Fragment key={key}>
          <dt className="text-muted-foreground">{key}</dt>
          <dd className="truncate text-foreground/90">{String(value)}</dd>
        </Fragment>
      ))}
    </dl>
  );
}
