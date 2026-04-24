import { Folder, Paperclip } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { InitialsAvatar } from "@/components/search/primitives/initials-avatar";
import { SnippetBody } from "@/components/search/primitives/snippet-body";
import { MetaRow } from "@/components/search/primitives/meta-row";
import { FileTypeIcon } from "@/components/search/primitives/file-type-icon";

interface AttachmentRef {
  id: string;
  filename: string;
}

interface Props {
  hit: DocumentHit;
  /** Click handler for an attachment chip. When provided (and the parent
   *  email was indexed with the `attachments: [{id, filename}]` metadata
   *  emitted by the IMAP connector), chips become buttons that trigger
   *  a direct download. */
  onAttachmentClick?: (att: AttachmentRef) => void;
}

// Email card content region. Four stacked bands so the card reads like a
// letter preview rather than a flat row of metadata:
//   1. Sender band — avatar + display name + address, plus a → recipient
//      routing line. Who sent what to whom is how people mentally index
//      mail, so this leads the composition.
//   2. Excerpt band — matched snippet inside a left-ruled block tinted in
//      the IMAP source hue. Reads as a quoted passage from the message.
//   3. Attachment shelf — filename chips with file-type glyphs; collapses
//      to a single "has attachments" pill when only the flag is set with
//      no filenames.
//   4. Meta footer — folder (non-INBOX only) via MetaRow.
// The chassis above supplies the subject h3 and the relative timestamp.

function str(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() ? v : undefined;
}

function strArr(v: unknown): string[] {
  return Array.isArray(v)
    ? v.filter((x): x is string => typeof x === "string")
    : [];
}

// parseAddress extracts {name, address} from 'Alice Smith <alice@ex.com>'
// or bare variants. Tolerates the quoted display names that Go's net/mail
// emits.
function parseAddress(raw: string): {
  name: string | null;
  address: string | null;
} {
  const trimmed = raw.trim();
  const m = /^(.*?)\s*<([^>]+)>\s*$/.exec(trimmed);
  if (m) {
    const name = m[1].replace(/^"(.*)"$/, "$1").trim();
    return { name: name || null, address: m[2].trim() || null };
  }
  if (trimmed.includes("@")) return { name: null, address: trimmed };
  return { name: trimmed, address: null };
}

function recipientSummary(raw: string): {
  first: { name: string | null; address: string | null };
  more: number;
} {
  const parts = raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  return {
    first: parseAddress(parts[0] || raw),
    more: Math.max(0, parts.length - 1),
  };
}

function extOf(name: string): string {
  const i = name.lastIndexOf(".");
  return i > 0 ? name.slice(i + 1) : "";
}

// parseAttachmentRefs reads the enriched `attachments: [{id, filename}]`
// array the IMAP connector emits on each parent email. Returns [] for
// legacy documents indexed before that metadata existed.
function parseAttachmentRefs(v: unknown): AttachmentRef[] {
  if (!Array.isArray(v)) return [];
  const out: AttachmentRef[] = [];
  for (const item of v) {
    if (item && typeof item === "object") {
      const rec = item as Record<string, unknown>;
      const id = typeof rec.id === "string" ? rec.id : "";
      const filename = typeof rec.filename === "string" ? rec.filename : "";
      if (id && filename) out.push({ id, filename });
    }
  }
  return out;
}

export function EmailCardBody({
  hit,
  onAttachmentClick,
}: Readonly<Props>) {
  const m = hit.metadata ?? {};
  const fromRaw = str(m.from);
  const toRaw = str(m.to);
  const ccRaw = str(m.cc);
  const folder = str(m.folder);
  const attachmentRefs = parseAttachmentRefs(m.attachments);
  // Fall back to legacy `attachment_filenames` for docs indexed before
  // the `attachments` metadata shipped — they render as static chips.
  const attachments =
    attachmentRefs.length > 0
      ? attachmentRefs.map((a) => a.filename)
      : strArr(m.attachment_filenames);
  const hasAttachmentsFlag = m.has_attachments === true;

  const from = fromRaw ? parseAddress(fromRaw) : null;
  const to = toRaw ? recipientSummary(toRaw) : null;
  const hasCC = !!ccRaw;
  const showFolder = folder && folder !== "INBOX";
  const hasSnippet = !!(hit.headline || hit.content);
  const fallbackAttPill = attachments.length === 0 && hasAttachmentsFlag;

  if (
    !from &&
    !to &&
    !showFolder &&
    !hasSnippet &&
    attachments.length === 0 &&
    !hasAttachmentsFlag
  ) {
    return null;
  }

  const avatarName = from?.name || from?.address || fromRaw;
  const avatarSeed = from?.address || fromRaw || "unknown";

  return (
    <div className="mt-2.5 flex flex-col gap-3">
      {(from || to) && (
        <div className="flex items-start gap-3">
          {from && (
            <InitialsAvatar
              name={avatarName}
              seed={avatarSeed}
              size={32}
              className="mt-0.5"
            />
          )}
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            {from && (
              <div className="flex min-w-0 items-baseline gap-2">
                <span className="truncate text-[13.5px] font-medium leading-tight text-foreground">
                  {from.name || from.address || "Unknown sender"}
                </span>
                {from.name && from.address && (
                  <span className="truncate text-[12px] leading-tight text-muted-foreground/80">
                    {from.address}
                  </span>
                )}
              </div>
            )}
            {to && (
              <div className="flex min-w-0 items-center gap-1.5 text-[12px] leading-tight text-muted-foreground">
                <span
                  aria-hidden
                  className="shrink-0 text-[color:var(--source-imap)]/70"
                >
                  →
                </span>
                <span className="truncate">
                  {to.first.name || to.first.address || "unknown"}
                </span>
                {to.more > 0 && (
                  <span
                    className="shrink-0 rounded-sm bg-muted/40 px-1 text-[10.5px] font-medium tabular-nums text-muted-foreground/80"
                    title={toRaw}
                  >
                    +{to.more}
                  </span>
                )}
                {hasCC && (
                  <span
                    className="shrink-0 rounded-sm bg-muted/40 px-1 text-[10.5px] font-medium uppercase tracking-wide text-muted-foreground/70"
                    title="also sent to cc"
                  >
                    cc
                  </span>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {hasSnippet && (
        <div
          style={{
            borderLeftColor:
              "color-mix(in oklch, var(--source-imap) 50%, transparent)",
            backgroundColor:
              "color-mix(in oklch, var(--source-imap) 4%, transparent)",
          }}
          className="rounded-r-md border-l-2 px-3 py-2"
        >
          <SnippetBody hit={hit} lineClamp={3} />
        </div>
      )}

      {attachments.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          {attachmentRefs.length > 0 && onAttachmentClick
            ? attachmentRefs.map((ref) => (
                <button
                  key={ref.id}
                  type="button"
                  title={`Download ${ref.filename}`}
                  onClick={() => onAttachmentClick(ref)}
                  style={{
                    backgroundColor:
                      "color-mix(in oklch, var(--source-imap) 5%, transparent)",
                    borderColor:
                      "color-mix(in oklch, var(--source-imap) 22%, var(--border))",
                  }}
                  className="group/att inline-flex h-6 max-w-[16rem] shrink-0 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-[11.5px] transition-colors hover:bg-[color-mix(in_oklch,var(--source-imap)_10%,transparent)]"
                >
                  <FileTypeIcon
                    extension={extOf(ref.filename)}
                    className="size-3.5 shrink-0 text-[color:var(--source-imap)]"
                  />
                  <span className="truncate text-foreground/85 group-hover/att:text-foreground">
                    {ref.filename}
                  </span>
                </button>
              ))
            : attachments.map((name) => (
                <span
                  key={name}
                  title={name}
                  style={{
                    backgroundColor:
                      "color-mix(in oklch, var(--source-imap) 5%, transparent)",
                    borderColor:
                      "color-mix(in oklch, var(--source-imap) 22%, var(--border))",
                  }}
                  className="inline-flex h-6 max-w-[16rem] shrink-0 items-center gap-1.5 rounded-md border px-2 text-[11.5px]"
                >
                  <FileTypeIcon
                    extension={extOf(name)}
                    className="size-3.5 shrink-0 text-[color:var(--source-imap)]"
                  />
                  <span className="truncate text-foreground/85">{name}</span>
                </span>
              ))}
        </div>
      )}

      {fallbackAttPill && (
        <div className="flex">
          <span
            style={{
              backgroundColor:
                "color-mix(in oklch, var(--source-imap) 5%, transparent)",
              borderColor:
                "color-mix(in oklch, var(--source-imap) 22%, var(--border))",
            }}
            className="inline-flex h-6 items-center gap-1.5 rounded-md border px-2 text-[11.5px] text-muted-foreground"
          >
            <Paperclip
              className="size-3 text-[color:var(--source-imap)]"
              aria-hidden
            />
            has attachments
          </span>
        </div>
      )}

      <MetaRow
        items={[
          showFolder && folder
            ? {
                key: "folder",
                icon: Folder,
                label: folder,
              }
            : false,
        ]}
      />
    </div>
  );
}
