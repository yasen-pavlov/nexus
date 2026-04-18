import { format } from "date-fns";
import { ArrowRight, MessageCircle, Sparkles } from "lucide-react";
import type { DocumentHit, MessageLine } from "@/lib/api-types";
import { Button } from "@/components/ui/button";
import { SenderAvatar } from "@/components/conversation/sender-avatar";
import { useAvatarBlob } from "@/hooks/use-avatar-blob";
import { useIdentities } from "@/hooks/use-identities";
import { cn } from "@/lib/utils";

function str(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() ? v : undefined;
}

function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

function readMessageLines(hit: DocumentHit): MessageLine[] | null {
  const raw = hit.metadata?.message_lines;
  if (!Array.isArray(raw)) return null;
  const lines: MessageLine[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "object") continue;
    const e = entry as Record<string, unknown>;
    const id = num(e.id);
    const text = typeof e.text === "string" ? e.text : "";
    const createdAt = typeof e.created_at === "string" ? e.created_at : "";
    if (id === undefined) continue;
    lines.push({
      id,
      text,
      created_at: createdAt,
      sender_id: num(e.sender_id),
      sender_name: str(e.sender_name),
      sender_username: str(e.sender_username),
      sender_avatar_key: str(e.sender_avatar_key),
    });
  }
  return lines.length > 0 ? lines : null;
}

function truncate(s: string, n: number): string {
  const one = s.replace(/\s+/g, " ").trim();
  if (one.length <= n) return one;
  return one.slice(0, n - 1).trimEnd() + "…";
}

interface Props {
  hit: DocumentHit;
  onOpenChat?: (hit: DocumentHit) => void;
}

export function TelegramCardBody({ hit, onOpenChat }: Props) {
  const matchSourceID = hit.match_source_id;
  const messageLines = readMessageLines(hit);

  if (matchSourceID) {
    return <MatchCard hit={hit} onOpenChat={onOpenChat} />;
  }
  if (messageLines && messageLines.length >= 2) {
    return (
      <SemanticCard hit={hit} lines={messageLines} onOpenChat={onOpenChat} />
    );
  }
  return <LegacyTail hit={hit} onOpenChat={onOpenChat} />;
}

// MatchCard renders a pinpoint message — avatar + sender + timestamp +
// highlighted snippet — mirroring the MessageRow chrome used in the
// conversation view. "Open in chat" jumps straight to that message.
function MatchCard({ hit, onOpenChat }: Props) {
  const { bySourceType } = useIdentities();
  const self = bySourceType.get("telegram");
  const isSelf =
    !!self &&
    hit.match_sender_id !== undefined &&
    String(hit.match_sender_id) === self.external_id;

  const senderName = hit.match_sender_name || "Unknown";
  const seed =
    hit.match_sender_id !== undefined
      ? String(hit.match_sender_id)
      : senderName;
  // Always try the avatar fetch when we know the sender and have a
  // telegram self-identity (which owns the avatar cache). The backend
  // 404s when there's no cached photo, which SenderAvatar gracefully
  // falls back from — don't gate on match_avatar_key alone, since it
  // may be absent on corner-case line entries (e.g. sync ordering)
  // while the photo is still cached.
  const avatarConnectorID =
    hit.match_sender_id !== undefined ? self?.connector_id ?? null : null;
  const avatarExternalID =
    hit.match_sender_id !== undefined ? String(hit.match_sender_id) : null;

  const chatName = str(hit.metadata?.chat_name) ?? hit.title ?? "Telegram chat";
  const messageCount = num(hit.metadata?.message_count);

  return (
    <div className="flex gap-3">
      <div className="shrink-0 pt-0.5">
        <ConnectedAvatar
          connectorId={avatarConnectorID}
          externalId={avatarExternalID}
          senderName={senderName}
          seed={seed}
          size={32}
        />
      </div>

      <div className="min-w-0 flex-1">
        <div className="mb-0.5 flex items-baseline gap-1.5 text-[12.5px]">
          <span
            className={cn(
              "truncate text-[14px] font-medium tracking-[-0.005em]",
              isSelf ? "text-primary" : "text-foreground",
            )}
          >
            {senderName}
          </span>
          {hit.match_created_at && (
            <span className="ml-auto shrink-0 pl-2 text-[11.5px] tabular-nums text-muted-foreground/80">
              {formatMatchTimestamp(hit.match_created_at)}
            </span>
          )}
        </div>

        {hit.headline ? (
          <div
            className={cn(
              "rounded-md px-3 py-2 text-[14px] leading-[21px]",
              isSelf
                ? "bg-primary/[0.07] text-foreground"
                : "bg-muted/30 text-foreground/95",
              "[&_mark]:rounded-sm [&_mark]:bg-primary/20 [&_mark]:px-0.5 [&_mark]:font-medium [&_mark]:text-foreground",
              "[&_em]:rounded-sm [&_em]:bg-primary/20 [&_em]:px-0.5 [&_em]:font-medium [&_em]:not-italic [&_em]:text-foreground",
            )}
            dangerouslySetInnerHTML={{ __html: hit.headline }}
          />
        ) : (
          <div
            className={cn(
              "rounded-md px-3 py-2 text-[14px] leading-[21px]",
              isSelf
                ? "bg-primary/[0.07] text-foreground"
                : "bg-muted/30 text-foreground/95",
            )}
          >
            {truncate(hit.content ?? "", 240) || "(no text)"}
          </div>
        )}

        <div className="mt-2 flex items-center justify-between gap-3 text-[12px] text-muted-foreground">
          <div className="flex min-w-0 items-center gap-1.5">
            <MessageCircle
              className="size-3.5 shrink-0 text-muted-foreground/70"
              aria-hidden
            />
            <span className="truncate">
              in <span className="font-medium text-foreground/80">{chatName}</span>
              {messageCount !== undefined && messageCount > 1 && (
                <>
                  <span className="mx-1 text-muted-foreground/50">·</span>
                  <span className="tabular-nums">{messageCount} in thread</span>
                </>
              )}
            </span>
          </div>
          {onOpenChat && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => onOpenChat(hit)}
              className="h-7 shrink-0 gap-1.5 rounded-md px-2.5 text-[12.5px] font-medium"
            >
              Open in chat
              <ArrowRight className="size-3.5" aria-hidden />
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

// SemanticCard renders the bookended fallback: first + last message of
// the window as compact rows, with a small "semantic match" pill. Used
// when the match couldn't be pinpointed to a single message (pure
// kNN/embedding hit with no BM25 highlight).
function SemanticCard({
  hit,
  lines,
  onOpenChat,
}: {
  hit: DocumentHit;
  lines: MessageLine[];
  onOpenChat?: (hit: DocumentHit) => void;
}) {
  const first = lines[0]!;
  const last = lines[lines.length - 1]!;
  const chatName = str(hit.metadata?.chat_name) ?? hit.title ?? "Telegram chat";
  const messageCount = num(hit.metadata?.message_count) ?? lines.length;

  const spanLabel = buildSpanLabel(first.created_at, last.created_at);

  const showSingle = lines.length < 2 || first.id === last.id;

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center gap-2 text-[12.5px] text-muted-foreground">
        <MessageCircle
          className="size-3.5 shrink-0 text-muted-foreground/70"
          aria-hidden
        />
        <span className="min-w-0 truncate font-medium text-foreground/90">
          {chatName}
        </span>
        <span className="shrink-0 text-muted-foreground/50">·</span>
        <span className="shrink-0 tabular-nums">{messageCount} messages</span>
        {spanLabel && (
          <>
            <span className="shrink-0 text-muted-foreground/50">·</span>
            <span className="shrink-0 tabular-nums">{spanLabel}</span>
          </>
        )}
        <span className="ml-auto inline-flex shrink-0 items-center gap-1 rounded-full bg-muted/70 px-2 py-0.5 text-[10.5px] font-semibold uppercase tracking-[0.06em] text-muted-foreground/90">
          <Sparkles className="size-3" aria-hidden />
          semantic match
        </span>
      </div>

      <div className="flex flex-col gap-1.5 rounded-md border border-border/70 bg-muted/20 p-2.5">
        <MiniLine line={first} />
        {!showSingle && (
          <>
            {lines.length > 2 && (
              <div className="pl-10 text-[11.5px] italic text-muted-foreground/70">
                …and {lines.length - 2} more
              </div>
            )}
            <MiniLine line={last} />
          </>
        )}
      </div>

      {onOpenChat && (
        <div className="flex justify-end">
          <Button
            variant="outline"
            size="sm"
            onClick={() => onOpenChat(hit)}
            className="h-7 shrink-0 gap-1.5 rounded-md px-2.5 text-[12.5px] font-medium"
          >
            Open in chat
            <ArrowRight className="size-3.5" aria-hidden />
          </Button>
        </div>
      )}
    </div>
  );
}

function MiniLine({ line }: { line: MessageLine }) {
  const { bySourceType } = useIdentities();
  const self = bySourceType.get("telegram");
  const isSelf =
    !!self &&
    line.sender_id !== undefined &&
    String(line.sender_id) === self.external_id;

  const seed =
    line.sender_id !== undefined ? String(line.sender_id) : line.sender_name || "anon";
  const avatarConnectorID = line.sender_avatar_key ? self?.connector_id : null;
  const avatarExternalID =
    line.sender_avatar_key && line.sender_id !== undefined
      ? String(line.sender_id)
      : null;
  const senderName = line.sender_name || "Unknown";

  return (
    <div className="flex min-w-0 items-baseline gap-2">
      <div className="shrink-0 self-start pt-0.5">
        <ConnectedAvatar
          connectorId={avatarConnectorID}
          externalId={avatarExternalID}
          senderName={senderName}
          seed={seed}
          size={24}
        />
      </div>
      <div className="min-w-0 flex-1">
        <span
          className={cn(
            "text-[13px] font-medium",
            isSelf ? "text-primary" : "text-foreground/90",
          )}
        >
          {senderName}
        </span>
        <span className="ml-2 text-[13px] text-muted-foreground">
          {truncate(line.text || "", 180)}
        </span>
      </div>
    </div>
  );
}

// ConnectedAvatar wires up the authed avatar blob fetch when an
// external ID + connector are available. Falls back to the presentational
// SenderAvatar's initials path otherwise. Mirrors the pattern used in
// MessageRow.
function ConnectedAvatar({
  connectorId,
  externalId,
  senderName,
  seed,
  size = 28,
}: {
  connectorId: string | null | undefined;
  externalId: string | null | undefined;
  senderName: string;
  seed: string;
  size?: number;
}) {
  const { data } = useAvatarBlob(connectorId, externalId);
  return (
    <SenderAvatar
      blobUrl={data ?? null}
      senderName={senderName}
      seed={seed}
      size={size}
    />
  );
}

// LegacyTail is the pre-reindex fallback: a compact row with chat
// name + "Open in chat". Matches the card body shipped before this
// change so legacy-indexed docs don't regress.
function LegacyTail({ hit, onOpenChat }: Props) {
  const chatName = str(hit.metadata?.chat_name);
  const messageCount = num(hit.metadata?.message_count);
  const canOpen = !!hit.conversation_id && !!onOpenChat;

  if (!chatName && messageCount === undefined && !canOpen) return null;

  return (
    <div className="mt-2 flex items-center justify-between gap-3 text-[13px]">
      <div className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
        <MessageCircle
          className="size-3.5 shrink-0 text-muted-foreground/70"
          aria-hidden
        />
        {chatName ? (
          <span className="truncate font-medium text-foreground/90">
            {chatName}
          </span>
        ) : (
          <span className="truncate">Telegram chat</span>
        )}
        {messageCount !== undefined && messageCount > 1 && (
          <>
            <span className="shrink-0 text-muted-foreground/50">·</span>
            <span className="shrink-0 tabular-nums">
              {messageCount} messages
            </span>
          </>
        )}
      </div>

      {canOpen && (
        <Button
          variant="outline"
          size="sm"
          onClick={() => onOpenChat?.(hit)}
          className="h-7 shrink-0 gap-1.5 rounded-md px-2.5 text-[12.5px] font-medium"
        >
          Open in chat
          <ArrowRight className="size-3.5" aria-hidden />
        </Button>
      )}
    </div>
  );
}

function formatMatchTimestamp(iso: string): string {
  try {
    const d = new Date(iso);
    return format(d, "MMM d · h:mm a");
  } catch {
    return iso;
  }
}

function buildSpanLabel(
  firstISO: string,
  lastISO: string,
): string | undefined {
  try {
    const a = new Date(firstISO);
    const b = new Date(lastISO);
    if (Number.isNaN(a.getTime()) || Number.isNaN(b.getTime())) return undefined;
    const sameDay = a.toDateString() === b.toDateString();
    if (sameDay) return format(a, "MMM d");
    return `${format(a, "MMM d")} – ${format(b, "MMM d")}`;
  } catch {
    return undefined;
  }
}
