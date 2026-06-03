// Thumbs feedback on an assistant message. PUTs to the message-feedback
// endpoint and refreshes the owning chat so the rating sticks across
// reloads. Pass feedback=null to clear an existing rating.

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { fetchAPI } from "@/lib/api-client";
import { chatKeys } from "@/lib/query-keys";

export type Feedback = "up" | "down" | null;

export interface MessageFeedbackInput {
  chatId: string;
  messageId: string;
  feedback: Feedback;
}

export function useMessageFeedback() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ chatId, messageId, feedback }: MessageFeedbackInput) =>
      fetchAPI<void>(
        `/api/chats/${chatId}/messages/${messageId}/feedback`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ feedback }),
        },
      ),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({
        queryKey: chatKeys.detail(variables.chatId),
      });
    },
  });
}
