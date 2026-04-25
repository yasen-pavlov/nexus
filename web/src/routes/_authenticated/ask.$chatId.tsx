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
  return <ChatThread chatID={chatId} initialContent={q} />;
}
