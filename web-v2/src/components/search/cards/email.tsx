import { Paperclip } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";

function str(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() ? v : undefined;
}

function strArr(v: unknown): string[] {
  return Array.isArray(v)
    ? v.filter((x): x is string => typeof x === "string")
    : [];
}

export function EmailCardBody({ hit }: { hit: DocumentHit }) {
  const m = hit.metadata ?? {};
  const from = str(m.from);
  const to = str(m.to);
  const folder = str(m.folder);
  const attachments = strArr(m.attachment_filenames);
  const hasAttachmentsFlag = m.has_attachments === true;
  const showFolder = folder && folder !== "INBOX";
  const showAttachments = attachments.length > 0 || hasAttachmentsFlag;

  if (!from && !to && !showFolder && !showAttachments) return null;

  return (
    <div className="mt-2 flex flex-col gap-1.5 text-[13px]">
      {(from || to) && (
        <div className="flex min-w-0 items-baseline gap-1.5 text-muted-foreground">
          {from && (
            <span className="truncate text-foreground/90">{from}</span>
          )}
          {from && to && (
            <span className="shrink-0 text-muted-foreground/60">→</span>
          )}
          {to && <span className="truncate">{to}</span>}
        </div>
      )}

      {(showFolder || showAttachments) && (
        <div className="flex flex-wrap items-center gap-1.5">
          {showFolder && (
            <span className="inline-flex h-5 shrink-0 items-center gap-1 rounded-full border border-border bg-muted/40 px-2 text-[11.5px] font-medium text-muted-foreground">
              {folder}
            </span>
          )}
          {attachments.map((name) => (
            <span
              key={name}
              title={name}
              className="inline-flex h-5 max-w-[14rem] shrink-0 items-center gap-1 rounded-full border border-border bg-muted/40 px-2 text-[11.5px] text-muted-foreground"
            >
              <Paperclip
                className="size-3 shrink-0 text-muted-foreground/80"
                aria-hidden
              />
              <span className="truncate">{name}</span>
            </span>
          ))}
          {attachments.length === 0 && hasAttachmentsFlag && (
            <span className="inline-flex h-5 shrink-0 items-center gap-1 rounded-full border border-border bg-muted/40 px-2 text-[11.5px] text-muted-foreground">
              <Paperclip className="size-3" aria-hidden />
              attachments
            </span>
          )}
        </div>
      )}
    </div>
  );
}
