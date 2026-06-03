import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Sparkles } from "lucide-react";
import { toast } from "sonner";

import { Skeleton } from "@/components/ui/skeleton";
import {
  useChats,
  useCreateChat,
  useDeleteChat,
  useUpdateChat,
} from "@/hooks/use-chats";
import { useLLMDefault, useLLMModels } from "@/hooks/use-llm-models";
import { cn } from "@/lib/utils";

import { AskComposer } from "./ask-composer";
import { ExamplePill } from "./example-pill";
import { pickInitialModel } from "./pick-initial-model";
import { RecentChatItem } from "./recent-chat-item";

export interface AskLandingProps {
  initialQuery?: string;
}

const RECENT_LIMIT = 8;
const LAST_MODEL_KEY = "nexus_last_used_model";

const EXAMPLES = [
  "Summarise the last week of Anthropic invoices.",
  "Where did I see the keyword 'compliance' across Paperless and email?",
  "What did the team agree on Telegram about deploys?",
  "Find all PDFs touching `migrations/` in the last month.",
];

/**
 * /ask landing. Two stacked regions: hero (medallion + composer +
 * example pills) and recent chats. Carries the search-bar handoff: if
 * `initialQuery` is set on mount, silently creates a chat and routes
 * to /ask/{id}?q=... so the question fires immediately.
 */
export function AskLanding({ initialQuery }: Readonly<AskLandingProps>) {
  const navigate = useNavigate();
  const create = useCreateChat();
  const del = useDeleteChat();
  const rename = useUpdateChat();
  const modelsQuery = useLLMModels();
  const models = useMemo(() => modelsQuery.data ?? [], [modelsQuery.data]);
  const defaultQuery = useLLMDefault();
  const systemDefault = defaultQuery.data?.default_model ?? null;
  const chats = useChats(RECENT_LIMIT, 0);

  const [model, setModel] = useState("");
  const [prefill, setPrefill] = useState("");
  const handedOff = useRef(false);
  // Skeleton while we're creating the chat for the search-bar handoff.
  // The window after the mutation resolves and before `navigate()`
  // commits is bounded by a single render and not worth a separate
  // flag.
  const handingOff = !!initialQuery && create.isPending;

  // Initialise the default model during render once we have data —
  // matches the search-bar URL-mirror pattern and avoids the
  // setState-in-effect lint.
  const [defaultsKey, setDefaultsKey] = useState<string>("");
  const nextDefaultsKey = `${models.length}|${systemDefault ?? ""}`;
  if (defaultsKey !== nextDefaultsKey && models.length > 0) {
    setDefaultsKey(nextDefaultsKey);
    const lastUsed =
      typeof globalThis.localStorage !== "undefined"
        ? globalThis.localStorage.getItem(LAST_MODEL_KEY)
        : null;
    setModel(pickInitialModel(undefined, models, lastUsed, systemDefault));
  }

  // Search-bar handoff: create a chat + navigate immediately.
  useEffect(() => {
    if (!initialQuery || handedOff.current) return;
    if (models.length === 0) return; // wait until we know what model to default to
    handedOff.current = true;

    const lastUsed =
      typeof globalThis.localStorage !== "undefined"
        ? globalThis.localStorage.getItem(LAST_MODEL_KEY)
        : null;
    const chosen = pickInitialModel(
      undefined,
      models,
      lastUsed,
      systemDefault,
    );

    void (async () => {
      try {
        const chat = await create.mutateAsync({ default_model: chosen });
        await navigate({
          to: "/ask/$chatId",
          params: { chatId: chat.id },
          search: { q: initialQuery },
          replace: true,
        });
      } catch (err) {
        toast.error(err instanceof Error ? err.message : "Failed to start chat");
        handedOff.current = false;
      }
    })();
  }, [initialQuery, models, systemDefault, create, navigate]);

  const onSubmit = async (content: string) => {
    if (!model) return;
    try {
      const chat = await create.mutateAsync({ default_model: model });
      if (typeof globalThis.localStorage !== "undefined") {
        globalThis.localStorage.setItem(LAST_MODEL_KEY, model);
      }
      await navigate({
        to: "/ask/$chatId",
        params: { chatId: chat.id },
        search: { q: content },
      });
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to start chat");
    }
  };

  if (handingOff) {
    return (
      <div className="mx-auto flex max-w-2xl flex-col items-center gap-4 p-6 pt-16 text-center">
        <Skeleton className="h-12 w-12 rounded-xl" />
        <Skeleton className="h-5 w-48" />
        <div className="text-[13px] text-muted-foreground">
          Starting your chat…
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-12 p-6 pb-24">
      <section className="flex flex-col items-center gap-5 pt-10 sm:pt-16 text-center">
        <div
          aria-hidden
          className="flex size-14 items-center justify-center rounded-2xl bg-primary/15 text-primary"
        >
          <Sparkles className="size-6" aria-hidden />
        </div>
        <div className="flex flex-col gap-2">
          <h1 className="text-[28px] font-semibold tracking-tight text-foreground">
            What can I help you find?
          </h1>
          <p className="text-[14px] text-muted-foreground">
            Grounded answers with citations from across your indexed sources.
          </p>
        </div>
        <div className="mt-2 flex flex-wrap justify-center gap-2">
          {EXAMPLES.map((p) => (
            <ExamplePill key={p} prompt={p} onPick={(t) => setPrefill(t)} />
          ))}
        </div>
        <div className="mt-4 w-full">
          <AskComposer
            key={prefill}
            model={model}
            onModelChange={setModel}
            models={models}
            onSubmit={onSubmit}
            initialContent={prefill}
            isFirstTurn={false}
          />
        </div>
      </section>

      <section className="flex flex-col gap-4">
        <header className="flex items-center gap-2">
          <span className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
            Recent
          </span>
          {chats.total > 0 && (
            <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground tabular-nums">
              {chats.total}
            </span>
          )}
        </header>

        {chats.isLoading ? (
          <div className="grid gap-3 sm:grid-cols-2">
            <Skeleton className="h-20 w-full rounded-lg" />
            <Skeleton className="h-20 w-full rounded-lg" />
          </div>
        ) : chats.chats.length === 0 ? (
          <div
            className={cn(
              "rounded-lg border border-dashed border-border bg-card/50 p-6 text-center text-[13px] text-muted-foreground",
            )}
          >
            No chats yet — your first one starts here.
          </div>
        ) : (
          <>
            <div className="grid gap-3 sm:grid-cols-2">
              {chats.chats.map((c) => (
                <RecentChatItem
                  key={c.id}
                  chat={c}
                  onDelete={(id) => del.mutateAsync(id)}
                  onRename={async (id, title) => {
                    await rename.mutateAsync({ id, title });
                  }}
                />
              ))}
            </div>
            {chats.total > RECENT_LIMIT && (
              <div className="text-[11px] text-muted-foreground/70">
                Showing {RECENT_LIMIT} most recent
              </div>
            )}
          </>
        )}
      </section>
    </div>
  );
}
