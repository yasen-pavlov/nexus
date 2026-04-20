import { createFileRoute, useRouter } from "@tanstack/react-router";
import { z } from "zod/v4";
import { ConversationPage } from "@/components/conversation/conversation-page";

// Search param schema: both anchor_id and anchor_ts are needed to open
// at a specific message — id identifies which message to highlight and
// scroll to, ts tells the client which window of messages to fetch
// around. When either is missing we open at the tail of the
// conversation.
export const Route = createFileRoute(
  "/_authenticated/conversations/$sourceType/$conversationId",
)({
  validateSearch: z.object({
    anchor_id: z.number().optional(),
    anchor_ts: z.string().optional(),
  }),
  component: ConversationRoute,
});

function ConversationRoute() {
  const { sourceType, conversationId } = Route.useParams();
  const { anchor_id, anchor_ts } = Route.useSearch();
  const router = useRouter();

  // Back returns to wherever the user came from (typically a filtered
  // search URL). Falls back to the index when the conversation route
  // was opened as the session's entry point (deep link, reload).
  const onBack = () => {
    if (globalThis.history.length > 1) {
      router.history.back();
      return;
    }
    router.navigate({ to: "/" });
  };

  return (
    <ConversationPage
      sourceType={sourceType}
      conversationId={conversationId}
      anchorId={anchor_id}
      anchorTs={anchor_ts}
      onBack={onBack}
    />
  );
}
