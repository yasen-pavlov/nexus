import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute(
  "/_authenticated/conversations/$sourceType/$conversationId",
)({
  component: ConversationPage,
});

function ConversationPage() {
  const { sourceType, conversationId } = Route.useParams();
  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <h1 className="text-2xl font-semibold">Conversation</h1>
      <p className="text-muted-foreground">
        {sourceType} / {conversationId} — Coming in Phase 2.
      </p>
    </div>
  );
}
