import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useChat } from "@/hooks/use-chats";
import { useChatStream } from "@/hooks/use-chat-stream";
import { useLLMDefault, useLLMModels } from "@/hooks/use-llm-models";
import type { ChatCitation, ChatMessage, ChunkPreview } from "@/lib/api-types";
import { chatKeys } from "@/lib/query-keys";
import { cn } from "@/lib/utils";

import { AskComposer } from "./ask-composer";
import { AssistantTurn } from "./assistant-turn";
import { PhaseChip } from "./phase-chip";
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

function readLastUsedModel(): string | null {
  if (typeof globalThis.localStorage === "undefined") return null;
  return globalThis.localStorage.getItem(LAST_MODEL_KEY);
}

export function ChatThread({ chatID, initialContent }: Readonly<ChatThreadProps>) {
  const queryClient = useQueryClient();
  const detail = useChat(chatID);
  const modelsQuery = useLLMModels();
  const models = useMemo(() => modelsQuery.data ?? [], [modelsQuery.data]);
  const defaultQuery = useLLMDefault();
  const systemDefault = defaultQuery.data?.default_model ?? null;

  const [model, setModel] = useState<string>("");
  const autoFiredRef = useRef(false);

  // Adjust state during render once we have what we need to choose a
  // default — avoids the setState-in-effect lint and matches the
  // search-bar's URL-mirror pattern. The chat-id key prevents stale
  // model selection when the route swaps to a different chat.
  const [defaultsKey, setDefaultsKey] = useState<string>("");
  const nextKey = `${chatID}|${detail.data?.chat.id ?? ""}|${models.length}|${systemDefault ?? ""}`;
  if (defaultsKey !== nextKey && models.length > 0) {
    setDefaultsKey(nextKey);
    setModel(
      pickInitialModel(
        detail.data?.chat,
        models,
        readLastUsedModel(),
        systemDefault,
      ),
    );
  }

  const stream = useChatStream(chatID);
  const turn = stream.turn;

  // After the BE persists the assistant message, refetch the chat
  // detail so the streaming card transitions into a normal persisted
  // turn (and the streaming reducer resets next time start() fires).
  // When the auto-title path fired this turn, also invalidate the
  // chat list so the recent-chats grid catches the new title without
  // a manual refetch.
  useEffect(() => {
    if (turn.phase !== "done" || !turn.messageID) return;
    queryClient.invalidateQueries({ queryKey: chatKeys.detail(chatID) });
    if (turn.autoTitle) {
      queryClient.invalidateQueries({ queryKey: chatKeys.lists() });
    }
  }, [turn.phase, turn.messageID, turn.autoTitle, chatID, queryClient]);

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

  if (detail.isPending) {
    return <ChatThreadSkeleton />;
  }
  if (detail.isError || !detail.data) {
    return <ChatNotFound />;
  }

  // While a turn is active OR has just finished, render it as the
  // "streaming" card and hide its persisted twin from the message list.
  const showStreaming = turn.phase !== "idle";
  const persistedMessages = showStreaming
    ? detail.data.messages.filter((m) => m.id !== turn.messageID && m.content !== turn.userContent)
    : detail.data.messages;
  const isFirstTurn = persistedMessages.length === 0 && turn.phase === "idle";
  const lastUserSeq = lastSeqOfRole(persistedMessages, "user");
  const lastAssistantSeq = lastSeqOfRole(persistedMessages, "assistant");

  const isStreaming = turn.phase === "retrieving" || turn.phase === "streaming";

  // Each turn carries its own evidence locally (cited-only filter
  // applied inside AssistantTurn via the per-turn AnswerStream footer).
  // No global rail / panel state — sources travel with their answer.
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <div className="flex flex-col gap-8">
        {persistedMessages.map((m, idx) => {
          // Walk backward from this assistant turn to find the user
          // message that triggered it. Used by AssistantTurn to label
          // the synthetic "Searched <query>" strip when the rewriter
          // didn't run (first turn or rewriter-disabled), so the strip
          // shows the literal question instead of falling back to the
          // generic "Searched the index" copy.
          let userQueryForStrip: string | undefined;
          if (m.role === "assistant") {
            for (let j = idx - 1; j >= 0; j--) {
              if (persistedMessages[j].role === "user") {
                userQueryForStrip = persistedMessages[j].content;
                break;
              }
            }
          }
          return (
            <Turn
              key={m.id}
              message={m}
              evidence={filterToCited(m.evidence ?? [], m.citations ?? [])}
              isLastUser={m.role === "user" && m.seq === lastUserSeq}
              isLastAssistant={m.role === "assistant" && m.seq === lastAssistantSeq}
              onRegenerate={undefined}
              prevTurnCreatedAt={
                idx > 0 ? persistedMessages[idx - 1].created_at : undefined
              }
              prevUserContent={userQueryForStrip}
              nextMessage={
                idx + 1 < persistedMessages.length
                  ? persistedMessages[idx + 1]
                  : undefined
              }
            />
          );
        })}

        {showStreaming && (
          <div className="flex flex-col gap-4">
            {/* The user content the streaming turn is answering */}
            {turn.userContent && (
              <UserTurn
                message={syntheticUserMessage(chatID, turn.userContent)}
              />
            )}

            <PhaseChip
              phase={turn.phase}
              userContent={turn.userContent}
              query={turn.query}
              skippedRetrieval={turn.skippedRetrieval}
              startedAtMs={turn.startedAt}
              rewriterFailureReason={turn.rewriterFailureReason}
            />

            <AssistantTurn
              streaming={turn}
              evidence={filterToCited(turn.evidence, turn.citations)}
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
  );
}

interface TurnProps {
  message: ChatMessage;
  evidence: ChunkPreview[];
  isLastUser: boolean;
  isLastAssistant: boolean;
  onRegenerate?: () => void;
  /** ISO timestamp of the immediately preceding message — used by
   *  AssistantTurn to derive a wall-clock duration label. */
  prevTurnCreatedAt?: string;
  /** The user message content this assistant turn answered. Used by
   *  AssistantTurn to label the synthetic "Searched <query>" strip
   *  when the rewriter didn't run. */
  prevUserContent?: string;
  /** The immediately-following message in the thread. UserTurn uses
   *  this to surface the assistant's persisted `rewritten_query` as a
   *  retroactive footnote ("rewritten as: …") under the user bubble. */
  nextMessage?: ChatMessage;
}

function Turn({
  message,
  evidence,
  onRegenerate,
  prevTurnCreatedAt,
  prevUserContent,
  nextMessage,
}: Readonly<TurnProps>) {
  if (message.role === "user") {
    return <UserTurn message={message} nextMessage={nextMessage} />;
  }
  return (
    <AssistantTurn
      message={message}
      evidence={evidence}
      onRegenerate={onRegenerate}
      prevTurnCreatedAt={prevTurnCreatedAt}
      prevUserContent={prevUserContent}
    />
  );
}

function lastSeqOfRole(msgs: ChatMessage[], role: ChatMessage["role"]): number {
  let max = -1;
  for (const m of msgs) if (m.role === role && m.seq > max) max = m.seq;
  return max;
}

// filterToCited returns the chunks whose DocID appears in `citations`.
// Cited-only contract: inline `[N]` pills, the bottom Sources footer
// chips, and the in-place expand cards all index into this filtered
// slice using the same 1-based positions. Uncited retrieval lives in
// the per-search <ToolTrace> strips above the answer body.
function filterToCited(
  chunks: ChunkPreview[],
  citations: ChatCitation[],
): ChunkPreview[] {
  if (citations.length === 0) return [];
  const cited = new Set<string>();
  for (const c of citations) cited.add(c.doc_id);
  return chunks.filter((c) => cited.has(c.id));
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
    <div className="flex flex-col gap-8">
      <Skeleton className="h-24 w-full rounded-lg" />
      <Skeleton className="h-40 w-full rounded-lg" />
      <Skeleton className="h-20 w-full rounded-xl" />
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
