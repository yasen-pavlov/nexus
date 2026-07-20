import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod/v4";

import { ChatThread } from "@/components/ask/chat-thread";

const askChatSearchSchema = z.object({
  // Carried over from /ask?q=… or the search-bar Ask handoff. When
  // present on a freshly created chat, the thread fires the first
  // message automatically.
  q: z.string().optional(),
});

export const Route = createFileRoute("/_authenticated/ask/$chatId")({
  validateSearch: askChatSearchSchema,
  component: ChatPage,
});

function ChatPage() {
  const { chatId } = Route.useParams();
  const { q } = Route.useSearch();
  return (
    <div className="mx-auto w-full max-w-6xl p-4 md:p-6">
      {/* key by chatId so a param-only /ask/A → /ask/B navigation remounts the
          thread. TanStack Router reuses the component on a param change, and
          useChatStream seeds its reducer once at mount — without the key, chat
          A's in-flight turn would leak into B and corrupt B's stream cache. */}
      <ChatThread key={chatId} chatID={chatId} initialContent={q} />
    </div>
  );
}
