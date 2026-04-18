import type { DocumentHit } from "@/lib/api-types";

function isDisplayable(v: unknown): v is string | number | boolean {
  return typeof v === "string" || typeof v === "number" || typeof v === "boolean";
}

export function DefaultCardBody({ hit }: { hit: DocumentHit }) {
  const entries = Object.entries(hit.metadata ?? {}).filter(
    (entry): entry is [string, string | number | boolean] =>
      isDisplayable(entry[1]) && String(entry[1]).length > 0,
  );

  if (entries.length === 0) return null;

  return (
    <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
      {entries.slice(0, 6).map(([key, value]) => (
        <div key={key} className="col-span-2 grid grid-cols-subgrid">
          <dt className="font-mono text-muted-foreground">{key}</dt>
          <dd className="truncate text-foreground">{String(value)}</dd>
        </div>
      ))}
    </dl>
  );
}
