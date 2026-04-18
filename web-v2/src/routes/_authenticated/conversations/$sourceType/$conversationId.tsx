import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod/v4";

export const Route = createFileRoute(
  "/_authenticated/conversations/$sourceType/$conversationId",
)({
  validateSearch: z.object({
    anchor: z.number().optional(),
  }),
  component: ConversationPage,
});

function ConversationPage() {
  const { sourceType, conversationId } = Route.useParams();
  const { anchor } = Route.useSearch();
  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <h1 className="text-2xl font-semibold">Conversation</h1>
      <p className="text-muted-foreground">
        {sourceType} / {conversationId}
        {anchor !== undefined ? ` (anchor: ${anchor})` : ""} — Coming in Phase 2.
      </p>
    </div>
  );
}
