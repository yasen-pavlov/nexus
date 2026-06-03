// REST hooks around /api/chats. Streaming (POST /messages SSE) lives in
// use-chat-stream.ts — these wrap the boring CRUD half.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { fetchAPI } from "@/lib/api-client";
import type {
  Chat,
  ChatDetailResponse,
  ListChatsResponse,
} from "@/lib/api-types";
import { chatKeys } from "@/lib/query-keys";

export interface CreateChatInput {
  title?: string;
  default_model?: string;
}

export interface UpdateChatInput {
  id: string;
  title?: string;
  default_model?: string;
}

const DEFAULT_LIMIT = 50;

/** Recent-chats list. Polls lazily — chats only change on user action. */
export function useChats(limit = DEFAULT_LIMIT, offset = 0) {
  const list = useQuery<ListChatsResponse>({
    queryKey: chatKeys.list(limit, offset),
    queryFn: () =>
      fetchAPI<ListChatsResponse>(
        `/api/chats?limit=${limit}&offset=${offset}`,
      ),
    staleTime: 30_000,
  });

  return {
    chats: list.data?.chats ?? [],
    total: list.data?.total ?? 0,
    isLoading: list.isPending,
    error: list.error,
  };
}

/** Single chat + full message history. */
export function useChat(id: string | undefined) {
  return useQuery<ChatDetailResponse>({
    queryKey: chatKeys.detail(id ?? ""),
    queryFn: () => fetchAPI<ChatDetailResponse>(`/api/chats/${id}`),
    enabled: !!id,
    staleTime: 5_000,
  });
}

export function useCreateChat() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateChatInput) =>
      fetchAPI<Chat>("/api/chats", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: chatKeys.all });
    },
  });
}

export function useUpdateChat() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: UpdateChatInput) =>
      fetchAPI<Chat>(`/api/chats/${id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      }),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: chatKeys.all });
      queryClient.invalidateQueries({
        queryKey: chatKeys.detail(variables.id),
      });
    },
  });
}

export function useDeleteChat() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      fetchAPI<void>(`/api/chats/${id}`, { method: "DELETE" }),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: chatKeys.all });
      queryClient.removeQueries({ queryKey: chatKeys.detail(id) });
    },
  });
}
