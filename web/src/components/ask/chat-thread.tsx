import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useChat } from "@/hooks/use-chats";
import { useChatStream } from "@/hooks/use-chat-stream";
import { useLLMModels } from "@/hooks/use-llm-models";
import type { ChatMessage, ChunkPreview } from "@/lib/api-types";
import { chatKeys } from "@/lib/query-keys";
import { cn } from "@/lib/utils";

import { AskComposer } from "./ask-composer";
import { AssistantTurn } from "./assistant-turn";
import { EvidenceRail } from "./evidence-rail";
import { pickInitialModel } from "./pick-initial-model";
import { UserTurn } from "./user-turn";

export interface ChatThreadProps {
  chatID: string;
  /** Optional first-turn content carried from the search-bar /ask?q=…
   *  handoff. When set, the thread fires it as soon as the chat detail
   *  + models are ready, but only when there are no persisted
   *  messages yet (so revisiting `/ask/{id}?q=…` doesn't double-fire). */
  initialContent?: string;
}

const LAST_MODEL_KEY = "nexus_last_used_model";
const FLASH_DURATION_MS = 1500;

function readLastUsedModel(): string | null {
  if (typeof globalThis.localStorage === "undefined") return null;
  return globalThis.localStorage.getItem(LAST_MODEL_KEY);
}

export function ChatThread({ chatID, initialContent }: Readonly<ChatThreadProps>) {
  const queryClient = useQueryClient();
  const detail = useChat(chatID);
  const modelsQuery = useLLMModels();
  const models = useMemo(() => modelsQuery.data ?? [], [modelsQuery.data]);

  const [model, setModel] = useState<string>("");
  const autoFiredRef = useRef(false);
  // Lock the visible streaming turn to a local copy of evidence so a
  // pill click + scroll handshake works against the cards on screen.
  const [flashedDocID, setFlashedDocID] = useState<string | undefined>();

  // Adjust state during render once we have what we need to choose a
  // default — avoids the setState-in-effect lint and matches the
  // search-bar's URL-mirror pattern. The chat-id key prevents stale
  // model selection when the route swaps to a different chat.
  const [defaultsKey, setDefaultsKey] = useState<string>("");
  const nextKey = `${chatID}|${detail.data?.chat.id ?? ""}|${models.length}`;
  if (defaultsKey !== nextKey && models.length > 0) {
    setDefaultsKey(nextKey);
    setModel(pickInitialModel(detail.data?.chat, models, readLastUsedModel()));
  }

  const stream = useChatStream(chatID);
  const turn = stream.turn;

  // After the BE persists the assistant message, refetch the chat
  // detail so the streaming card transitions into a normal persisted
  // turn (and the streaming reducer resets next time start() fires).
  useEffect(() => {
    if (turn.phase !== "done" || !turn.messageID) return;
    queryClient.invalidateQueries({ queryKey: chatKeys.detail(chatID) });
  }, [turn.phase, turn.messageID, chatID, queryClient]);

  const handleSubmit = (content: string) => {
    if (!model) return;
    if (typeof globalThis.localStorage !== "undefined") {
      globalThis.localStorage.setItem(LAST_MODEL_KEY, model);
    }
    void stream.start({ content, model });
  };

  // Auto-fire the first turn from /ask?q=… when arriving on a
  // freshly-created chat. Guarded so revisits of an existing chat with
  // a stale `?q` don't replay.
  useEffect(() => {
    if (autoFiredRef.current) return;
    if (!initialContent) return;
    if (!model) return;
    if (!detail.data) return;
    if (detail.data.messages.length > 0) return;
    if (turn.phase !== "idle") return;
    autoFiredRef.current = true;
    handleSubmit(initialContent);
    // intentionally omit handleSubmit from deps — it captures `model`
    // which is already a dep, and we want the effect to fire once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialContent, model, detail.data, turn.phase]);

  const onJumpToEvidence = (docID: string) => {
    const el = document.querySelector(`[data-chunk-id="${docID}"]`);
    if (el && "scrollIntoView" in el) {
      (el as HTMLElement).scrollIntoView({ behavior: "smooth", block: "nearest" });
    }
    setFlashedDocID(docID);
    globalThis.setTimeout(() => setFlashedDocID(undefined), FLASH_DURATION_MS);
  };

  if (detail.isPending) {
    return <ChatThreadSkeleton />;
  }
  if (detail.isError || !detail.data) {
    return <ChatNotFound />;
  }

  // While a turn is active OR has just finished, render it as the
  // "streaming" card and hide its persisted twin from the message list.
  // This keeps the evidence rail populated after the orchestrator
  // persists the assistant message — chunks aren't stored on the row,
  // so the in-memory copy is the only place they live until the user
  // navigates away or starts a new turn.
  const showStreaming = turn.phase !== "idle";
  const persistedMessages = showStreaming
    ? detail.data.messages.filter((m) => m.id !== turn.messageID && m.content !== turn.userContent)
    : detail.data.messages;
  const isFirstTurn = persistedMessages.length === 0 && turn.phase === "idle";
  const lastUserSeq = lastSeqOfRole(persistedMessages, "user");
  const lastAssistantSeq = lastSeqOfRole(persistedMessages, "assistant");

  // The evidence rail tracks whichever turn is currently most relevant.
  const railEvidence = showStreaming ? turn.evidence : [];

  const isStreaming = turn.phase === "retrieving" || turn.phase === "streaming";
  const phaseLabel =
    turn.phase === "retrieving"
      ? "Searching your corpus"
      : turn.phase === "streaming"
        ? "Generating answer"
        : "";

  return (
    <div className="grid grid-cols-1 gap-6 md:grid-cols-[minmax(0,1fr)_360px]">
      <div className="flex min-w-0 flex-col gap-6">
        <div className="flex flex-col gap-8">
          {persistedMessages.map((m) => (
            <Turn
              key={m.id}
              message={m}
              evidence={[]}
              onJumpToEvidence={onJumpToEvidence}
              isLastUser={m.role === "user" && m.seq === lastUserSeq}
              isLastAssistant={m.role === "assistant" && m.seq === lastAssistantSeq}
              onRegenerate={undefined}
            />
          ))}

          {showStreaming && (
            <div className="flex flex-col gap-4">
              {/* The user content the streaming turn is answering */}
              {turn.userContent && (
                <UserTurn
                  message={syntheticUserMessage(chatID, turn.userContent)}
                />
              )}
              {phaseLabel && (
                <div
                  role="status"
                  aria-live="polite"
                  className="inline-flex items-center gap-2 self-start rounded-full bg-primary/10 px-3 py-1 text-[11px] font-medium text-primary"
                >
                  <span className="size-1.5 animate-pulse rounded-full bg-primary" aria-hidden />
                  {phaseLabel}
                </div>
              )}
              <AssistantTurn
                streaming={turn}
                evidence={turn.evidence}
                onJumpToEvidence={onJumpToEvidence}
                onRegenerate={
                  turn.phase === "error"
                    ? () => handleSubmit(turn.userContent)
                    : undefined
                }
              />
            </div>
          )}
        </div>

        <div className="sticky bottom-4">
          <AskComposer
            model={model}
            onModelChange={setModel}
            models={models}
            onSubmit={handleSubmit}
            isStreaming={isStreaming}
            onCancel={stream.cancel}
            isFirstTurn={isFirstTurn}
          />
        </div>
      </div>

      <EvidenceRail
        chunks={railEvidence}
        highlightedDocID={flashedDocID}
        onActivate={onJumpToEvidence}
      />
    </div>
  );
}

interface TurnProps {
  message: ChatMessage;
  evidence: ChunkPreview[];
  onJumpToEvidence: (docID: string) => void;
  isLastUser: boolean;
  isLastAssistant: boolean;
  onRegenerate?: () => void;
}

function Turn({ message, evidence, onJumpToEvidence, onRegenerate }: Readonly<TurnProps>) {
  if (message.role === "user") {
    return <UserTurn message={message} />;
  }
  return (
    <AssistantTurn
      message={message}
      evidence={evidence}
      onJumpToEvidence={onJumpToEvidence}
      onRegenerate={onRegenerate}
    />
  );
}

function lastSeqOfRole(msgs: ChatMessage[], role: ChatMessage["role"]): number {
  let max = -1;
  for (const m of msgs) if (m.role === role && m.seq > max) max = m.seq;
  return max;
}

function syntheticUserMessage(chatID: string, content: string): ChatMessage {
  return {
    id: `streaming-user-${chatID}`,
    chat_id: chatID,
    role: "user",
    seq: -1,
    content,
    created_at: new Date().toISOString(),
  };
}

function ChatThreadSkeleton() {
  return (
    <div className="grid grid-cols-1 gap-6 md:grid-cols-[minmax(0,1fr)_360px]">
      <div className="flex flex-col gap-8">
        <Skeleton className="h-24 w-full rounded-lg" />
        <Skeleton className="h-40 w-full rounded-lg" />
        <Skeleton className="h-20 w-full rounded-xl" />
      </div>
      <div className="hidden flex-col gap-2 md:flex">
        <Skeleton className="h-32 w-full rounded-lg" />
        <Skeleton className="h-32 w-full rounded-lg" />
      </div>
    </div>
  );
}

function ChatNotFound() {
  return (
    <div className={cn("flex flex-col items-center gap-4 py-16 text-center")}>
      <div className="flex size-12 items-center justify-center rounded-xl bg-primary/15 text-primary">
        <Sparkles className="size-5" aria-hidden />
      </div>
      <div className="flex flex-col gap-1">
        <div className="text-[15px] font-medium">Chat not found</div>
        <div className="text-[13px] text-muted-foreground">
          It may have been deleted, or you don't have access.
        </div>
      </div>
      <Button render={<Link to="/ask" />}>Back to Ask</Button>
    </div>
  );
}
