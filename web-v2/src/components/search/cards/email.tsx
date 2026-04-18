import { Mail, Paperclip, Folder } from "lucide-react";
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
    <div className="mt-2 flex flex-col gap-1.5 text-sm">
      {(from || to) && (
        <div className="flex items-baseline gap-2 text-muted-foreground">
          <Mail className="size-3.5 shrink-0 translate-y-0.5" aria-hidden />
          {from && (
            <span className="truncate font-medium text-foreground">{from}</span>
          )}
          {from && to && (
            <span className="shrink-0 text-muted-foreground/60">→</span>
          )}
          {to && <span className="truncate">{to}</span>}
        </div>
      )}

      {(showFolder || showAttachments) && (
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          {showFolder && (
            <span className="inline-flex items-center gap-1 rounded border border-border/80 px-1.5 py-0.5 font-medium text-muted-foreground">
              <Folder className="size-3" aria-hidden />
              {folder}
            </span>
          )}
          {attachments.map((name) => (
            <span
              key={name}
              className="inline-flex max-w-[14rem] items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-muted-foreground"
              title={name}
            >
              <Paperclip className="size-3 shrink-0" aria-hidden />
              <span className="truncate">{name}</span>
            </span>
          ))}
          {attachments.length === 0 && hasAttachmentsFlag && (
            <span className="inline-flex items-center gap-1 text-muted-foreground">
              <Paperclip className="size-3" aria-hidden />
              attachments
            </span>
          )}
        </div>
      )}
    </div>
  );
}
